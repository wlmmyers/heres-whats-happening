import { GetObjectCommand, S3Client } from "@aws-sdk/client-s3";
import { SQSClient } from "@aws-sdk/client-sqs";
import type { S3Event } from "aws-lambda";
import { gate, parseEmail } from "./email.js";
import { AwsSecretReader, MastraExtractor, loadModelKey, type EventExtractor } from "./extractor.js";
import { toMessage } from "./map.js";
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

/** AWS Lambda entrypoint: S3 ObjectCreated -> fetch raw email -> process. */
export async function handler(event: S3Event): Promise<void> {
  const secretArn = process.env.LLM_API_KEY_SECRET;
  if (secretArn) await loadModelKey(new AwsSecretReader(process.env.AWS_REGION), secretArn);
  const deps = prodDeps();
  const s3 = new S3Client({ region: process.env.AWS_REGION });
  // S3 ObjectCreated events contain one record each in practice; if a multi-record
  // batch ever arrives, a failure on record N re-processes 0..N-1 on retry (safe:
  // deterministic source_event_id + consumer upsert make re-sends idempotent).
  for (const rec of event.Records) {
    const bucket = rec.s3.bucket.name;
    const key = decodeURIComponent(rec.s3.object.key.replace(/\+/g, " "));
    const raw = await getObject(s3, bucket, key);
    await processEmail(raw, deps);
  }
}
