import { GetObjectCommand, S3Client } from '@aws-sdk/client-s3';
import { SQSClient } from '@aws-sdk/client-sqs';
import type { APIGatewayProxyEventV2, S3Event, SQSEvent } from 'aws-lambda';
import { gate, parseEmail } from './email.js';
import {
  AwsSecretReader,
  MastraExtractor,
  loadModelKey,
  type EventExtractor,
} from './extractor.js';
import { toMessage } from './map.js';
import {
  BadRequestError,
  parsePosterRequest,
  posterHttpResponse,
  processPosterRequest,
  type PosterDeps,
} from './poster.js';
import type { PosterRequest } from './poster-schema.js';
import { S3PosterSink } from './poster-sink.js';
import { artifactStore } from './mastra/tools/artifact-store.js';
import type { PosterWorkflowOutput } from './mastra/workflows/poster.schemas.js';
import { posterWorkflow } from './mastra/workflows/poster.workflow.js';
import { EventMessageSchema, type EventMessage } from './schema.js';
import { sendBatch } from './sqs.js';
import { enrichEvent, type EnrichDeps } from './enrichment.js';
import { S3EnrichmentCache } from './enrichment-cache.js';
import { prodBioDeps, enrichBio } from './enrich-bio.js';
import { prodTourDeps, enrichTour } from './enrich-tour.js';
import { prodImageDeps, enrichImage } from './enrich-image.js';
import { searchArtists } from './mastra/tools/musicbrainz.tool.js';

export interface ProcessDeps {
  extractor: EventExtractor;
  emit: (messages: EventMessage[]) => Promise<void>;
}

/** Core, dependency-injected pipeline for one raw email. Pure of AWS wiring so
 * it is unit-testable; the Lambda entrypoint supplies real deps. */
export async function processEmail(raw: Buffer, deps: ProcessDeps): Promise<void> {
  const parsed = await parseEmail(raw);
  const decision = gate(parsed);
  if (decision === 'skip') {
    console.log(
      JSON.stringify({ msg: 'skip', spamFail: parsed.spamFail, virusFail: parsed.virusFail }),
    );
    return;
  }
  const drafts = await deps.extractor.extract({
    mode: decision,
    text: parsed.text,
    images: parsed.images,
    receivedAt: parsed.date,
  });
  // Drop drafts missing the fields that define an event and seed the dedup hash.
  const valid = drafts.filter((d) => d.title.trim() !== '' && d.venue.name.trim() !== '');
  const dropped = drafts.length - valid.length;
  if (dropped > 0) console.log(JSON.stringify({ msg: 'dropped-invalid-drafts', dropped }));
  if (valid.length === 0) {
    console.log(JSON.stringify({ msg: 'no-events', mode: decision }));
    return;
  }
  await deps.emit(valid.map(toMessage));
  console.log(JSON.stringify({ msg: 'emitted', count: valid.length, mode: decision }));
}

function requireEnv(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`missing env var ${name}`);
  return v;
}

/** Build production deps from the environment. */
function prodDeps(): ProcessDeps {
  const region = requireEnv('AWS_REGION');
  const queueUrl = requireEnv('EVENTS_QUEUE_URL');
  const endpoint = process.env.SQS_ENDPOINT || undefined; // set for local/ElasticMQ
  const sqs = new SQSClient({ region, endpoint });
  return {
    extractor: new MastraExtractor(),
    emit: (messages) => sendBatch(sqs, queueUrl, messages),
  };
}

async function getObject(s3: S3Client, bucket: string, key: string): Promise<Buffer> {
  const out = await s3.send(new GetObjectCommand({ Bucket: bucket, Key: key }));
  const bytes = await out.Body!.transformToByteArray();
  return Buffer.from(bytes);
}

type HandlerEvent = S3Event | APIGatewayProxyEventV2 | SQSEvent;
interface HttpResponse {
  statusCode: number;
  headers: Record<string, string>;
  body: string;
}

/** True when the event is a Lambda Function URL (API GW v2 payload) request. */
export function isFunctionUrlEvent(event: HandlerEvent): event is APIGatewayProxyEventV2 {
  return (
    typeof (event as APIGatewayProxyEventV2).version === 'string' &&
    (event as APIGatewayProxyEventV2).version === '2.0' &&
    !!(event as APIGatewayProxyEventV2).requestContext?.http
  );
}

/** True when the invocation came from the SQS event source mapping.
 * Discriminates on eventSource, NOT on the presence of Records: the S3 branch
 * uses Records too, and the previous unguarded cast to S3Event would have
 * happily accepted an SQS event and failed confusingly deeper in. */
export function isSQSEvent(event: HandlerEvent): event is SQSEvent {
  const recs = (event as SQSEvent).Records;
  return Array.isArray(recs) && recs.length > 0 && recs[0]?.eventSource === 'aws:sqs';
}

/** Poster path: parse -> run -> map to HTTP. Returns 400/422/500 only — never throws. */
export async function handlePosterHttp(
  event: APIGatewayProxyEventV2,
  deps: PosterDeps,
): Promise<HttpResponse> {
  let req;
  try {
    req = parsePosterRequest(event.body, event.isBase64Encoded ?? false);
  } catch (e) {
    if (e instanceof BadRequestError) {
      return {
        statusCode: 400,
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ error: e.message }),
      };
    }
    console.error(
      JSON.stringify({
        msg: 'poster-parse-error',
        error: e instanceof Error ? e.message : String(e),
      }),
    );
    return {
      statusCode: 500,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ error: 'internal error' }),
    };
  }
  try {
    const result = await processPosterRequest(req, deps);
    return posterHttpResponse(result);
  } catch (e) {
    console.error(
      JSON.stringify({ msg: 'poster-error', error: e instanceof Error ? e.message : String(e) }),
    );
    return {
      statusCode: 500,
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ error: 'internal error' }),
    };
  }
}

export interface SQSDeps {
  enrich(message: EventMessage): Promise<unknown>;
  emit(messages: unknown[]): Promise<void>;
}

/** Enrichment path: one normalized event in, one enriched event out.
 * The event source mapping uses batch_size 1, so this loop sees a single
 * record in practice; it iterates anyway rather than assuming. */
export async function handleSQS(event: SQSEvent, deps: SQSDeps): Promise<void> {
  const out: unknown[] = [];
  for (const rec of event.Records) {
    let parsed: EventMessage;
    try {
      parsed = EventMessageSchema.parse(JSON.parse(rec.body));
    } catch (e) {
      // Unparseable bodies never become parseable. Throwing would return the
      // message and burn three enrichment attempts before the DLQ takes it.
      console.error(
        JSON.stringify({
          msg: 'enrichment-bad-message',
          messageId: rec.messageId,
          error: e instanceof Error ? e.message : String(e),
        }),
      );
      continue;
    }
    out.push(await deps.enrich(parsed));
  }
  if (out.length === 0) return;
  // A send failure throws: at batch_size 1 that returns the message to the
  // queue, which is this path's only retry mechanism.
  await deps.emit(out);
}

/** setlist.fm API key, loaded once at cold start and captured by value into
 * prodTourDeps() — see the handler body, which populates this before
 * prodEnrichDeps() is ever called. */
let setlistFmKey = '';

function prodEnrichDeps(): EnrichDeps {
  const bucket = requireEnv('ENRICHMENT_CACHE_BUCKET');
  const bio = prodBioDeps();
  const image = prodImageDeps();
  const tour = prodTourDeps(setlistFmKey);
  return {
    cache: new S3EnrichmentCache(new S3Client({ region: process.env.AWS_REGION }), bucket),
    searchArtists: (performer, opts) => searchArtists(performer, opts),
    enrichImage: (artist) => enrichImage(image, artist),
    enrichBio: (artist) => enrichBio(bio, artist),
    enrichTour: (artist, evt) => enrichTour(tour, artist, evt),
  };
}

/** Existing S3 -> email path (unchanged behavior), extracted for the branch. */
export async function handleS3(event: S3Event): Promise<void> {
  const deps = prodDeps();
  const s3 = new S3Client({ region: process.env.AWS_REGION });
  // S3 ObjectCreated events carry one record each in practice; if a multi-record
  // batch ever arrives, a failure on record N re-processes 0..N-1 on retry (safe:
  // deterministic source_event_id + consumer upsert make re-sends idempotent).
  for (const rec of event.Records) {
    const bucket = rec.s3.bucket.name;
    const key = decodeURIComponent(rec.s3.object.key.replace(/\+/g, ' '));
    const raw = await getObject(s3, bucket, key);
    await processEmail(raw, deps);
  }
}

/** Single Lambda entrypoint. Streaming-wrapped (required for the Function URL path);
 * S3 async invokes run the same code and the response stream is ignored. */
export const handler = awslambda.streamifyResponse(
  async (event: HandlerEvent, responseStream, _context): Promise<void> => {
    const secretArn = process.env.LLM_API_KEY_SECRET;
    if (secretArn) await loadModelKey(new AwsSecretReader(process.env.AWS_REGION), secretArn);

    const setlistSecret = process.env.SETLISTFM_API_KEY_SECRET;
    if (setlistSecret && !setlistFmKey) {
      setlistFmKey = await new AwsSecretReader(process.env.AWS_REGION).getSecretValue(
        setlistSecret,
      );
    }

    if (isFunctionUrlEvent(event)) {
      const res = await handlePosterHttp(event, prodPosterDeps());
      const stream = awslambda.HttpResponseStream.from(responseStream, {
        statusCode: res.statusCode,
        headers: res.headers,
      });
      stream.write(res.body);
      stream.end();
      return;
    }

    if (isSQSEvent(event)) {
      const region = requireEnv('AWS_REGION');
      const queueUrl = requireEnv('ENRICHED_EVENTS_QUEUE_URL');
      const sqs = new SQSClient({ region, endpoint: process.env.SQS_ENDPOINT || undefined });
      const enrichDeps = prodEnrichDeps();
      await handleSQS(event, {
        enrich: (m) => enrichEvent(enrichDeps, m),
        emit: (msgs) => sendBatch(sqs, queueUrl, msgs),
      });
      responseStream.end();
      return;
    }

    await handleS3(event as S3Event);
    responseStream.end();
  },
);

/**
 * Run the registered workflow to completion and return its controlled output.
 *
 * NEVER throws. A workflow that dies mid-run has usually already written files
 * into its run directory, and the caller's cleanup keys off `artifactDir` in this
 * return value — so a throw here both 500s the request and leaks scratch on
 * Lambda's persistent /tmp. This function creates the run, so it knows the runId
 * and can name that directory even when nothing else survives.
 */
export async function runPosterWorkflow(req: PosterRequest): Promise<PosterWorkflowOutput> {
  const run = await posterWorkflow.createRun();
  // Same derivation finalizeStep uses (artifactStore(runId).dir); computing a path
  // creates nothing, so this is safe for runs that never wrote a byte.
  const artifactDir = artifactStore(run.runId).dir;
  try {
    const result = await run.start({ inputData: req });
    if (result.status === 'success') return result.result as PosterWorkflowOutput;
    const detail = result.status === 'failed' ? result.error?.message : result.status;
    return { ok: false, reason: `poster workflow did not complete: ${detail}`, artifactDir };
  } catch (e) {
    const reason = e instanceof Error ? e.message : String(e);
    console.error(
      JSON.stringify({ msg: 'poster-workflow-threw', runId: run.runId, error: reason }),
    );
    return { ok: false, reason: `poster workflow failed: ${reason}`, artifactDir };
  }
}

function prodPosterDeps(): PosterDeps {
  const region = requireEnv('AWS_REGION');
  const bucket = requireEnv('POSTERS_BUCKET');
  return {
    runWorkflow: runPosterWorkflow,
    sink: new S3PosterSink(new S3Client({ region }), bucket),
  };
}
