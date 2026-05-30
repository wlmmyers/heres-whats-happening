/* Pipe a local .eml through the REAL extractor to ElasticMQ.
 * Usage: ANTHROPIC_API_KEY=... EVENTS_QUEUE_URL=... SQS_ENDPOINT=http://localhost:9324 \
 *        pnpm invoke-local path/to/email.eml
 * Requires `make queue-up` and a created queue. */
import { readFileSync } from "node:fs";
import { SQSClient } from "@aws-sdk/client-sqs";
import { MastraExtractor } from "../src/extractor.js";
import { processEmail } from "../src/handler.js";
import { sendBatch } from "../src/sqs.js";

const file = process.argv[2];
if (!file) throw new Error("usage: pnpm invoke-local <email.eml>");

const region = process.env.AWS_REGION ?? "us-east-1";
const queueUrl = process.env.EVENTS_QUEUE_URL;
if (!queueUrl) throw new Error("set EVENTS_QUEUE_URL");
const sqs = new SQSClient({ region, endpoint: process.env.SQS_ENDPOINT || undefined });

await processEmail(readFileSync(file), {
  extractor: new MastraExtractor(),
  emit: (msgs) => {
    console.log(JSON.stringify(msgs, null, 2));
    return sendBatch(sqs, queueUrl, msgs);
  },
});
console.log("done");
