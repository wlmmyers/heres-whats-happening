import { GetObjectCommand, S3Client } from "@aws-sdk/client-s3";
import { SQSClient } from "@aws-sdk/client-sqs";
import type { APIGatewayProxyEventV2, S3Event } from "aws-lambda";
import { gate, parseEmail } from "./email.js";
import { AwsSecretReader, MastraExtractor, loadModelKey, type EventExtractor } from "./extractor.js";
import { toMessage } from "./map.js";
import { BadRequestError, parsePosterRequest, posterHttpResponse, processPosterRequest, type PosterDeps } from "./poster.js";
import type { EventMessage } from "./schema.js";
import { sendBatch } from "./sqs.js";

export interface ProcessDeps {
  extractor: EventExtractor;
  emit: (messages: EventMessage[]) => Promise<void>;
}

/** Core, dependency-injected pipeline for one raw email. Pure of AWS wiring so
 * it is unit-testable; the Lambda entrypoint supplies real deps. */
export async function processEmail(raw: Buffer, deps: ProcessDeps): Promise<void> {
  const parsed = await parseEmail(raw);
  const decision = gate(parsed);
  if (decision === "skip") {
    console.log(JSON.stringify({ msg: "skip", spamFail: parsed.spamFail, virusFail: parsed.virusFail }));
    return;
  }
  const drafts = await deps.extractor.extract({
    mode: decision,
    text: parsed.text,
    images: parsed.images,
    receivedAt: parsed.date,
  });
  // Drop drafts missing the fields that define an event and seed the dedup hash.
  const valid = drafts.filter((d) => d.title.trim() !== "" && d.venue.name.trim() !== "");
  const dropped = drafts.length - valid.length;
  if (dropped > 0) console.log(JSON.stringify({ msg: "dropped-invalid-drafts", dropped }));
  if (valid.length === 0) {
    console.log(JSON.stringify({ msg: "no-events", mode: decision }));
    return;
  }
  await deps.emit(valid.map(toMessage));
  console.log(JSON.stringify({ msg: "emitted", count: valid.length, mode: decision }));
}

function requireEnv(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`missing env var ${name}`);
  return v;
}

/** Build production deps from the environment. */
function prodDeps(): ProcessDeps {
  const region = requireEnv("AWS_REGION");
  const queueUrl = requireEnv("EVENTS_QUEUE_URL");
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

type HandlerEvent = S3Event | APIGatewayProxyEventV2;
interface HttpResponse {
  statusCode: number;
  headers: Record<string, string>;
  body: string;
}

/** True when the event is a Lambda Function URL (API GW v2 payload) request. */
export function isFunctionUrlEvent(event: HandlerEvent): event is APIGatewayProxyEventV2 {
  return (
    typeof (event as APIGatewayProxyEventV2).version === "string" &&
    (event as APIGatewayProxyEventV2).version === "2.0" &&
    !!(event as APIGatewayProxyEventV2).requestContext?.http
  );
}

/** Poster path: parse -> run -> map to HTTP. Returns 400/422/500 only — never throws. */
export async function handlePosterHttp(event: APIGatewayProxyEventV2, deps: PosterDeps): Promise<HttpResponse> {
  let req;
  try {
    req = parsePosterRequest(event.body, event.isBase64Encoded ?? false);
  } catch (e) {
    if (e instanceof BadRequestError) {
      return { statusCode: 400, headers: { "content-type": "application/json" }, body: JSON.stringify({ error: e.message }) };
    }
    throw e;
  }
  try {
    const result = await processPosterRequest(req, deps);
    return posterHttpResponse(result);
  } catch (e) {
    console.error(JSON.stringify({ msg: "poster-error", error: e instanceof Error ? e.message : String(e) }));
    return { statusCode: 500, headers: { "content-type": "application/json" }, body: JSON.stringify({ error: "internal error" }) };
  }
}

/** Existing S3 -> email path (unchanged behavior), extracted for the branch. */
export async function handleS3(event: S3Event): Promise<void> {
  const deps = prodDeps();
  const s3 = new S3Client({ region: process.env.AWS_REGION });
  for (const rec of event.Records) {
    const bucket = rec.s3.bucket.name;
    const key = decodeURIComponent(rec.s3.object.key.replace(/\+/g, " "));
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

    if (isFunctionUrlEvent(event)) {
      const res = await handlePosterHttp(event, prodPosterDeps());
      const stream = awslambda.HttpResponseStream.from(responseStream, { statusCode: res.statusCode, headers: res.headers });
      stream.write(res.body);
      stream.end();
      return;
    }

    await handleS3(event as S3Event);
    responseStream.end();
  },
);

// TEMP — replaced with the real implementation in Task 11.
function prodPosterDeps(): PosterDeps {
  throw new Error("prodPosterDeps not yet wired (Task 11)");
}
