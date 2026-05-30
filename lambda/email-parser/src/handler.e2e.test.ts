import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { join } from "node:path";
import {
  CreateQueueCommand,
  DeleteQueueCommand,
  ReceiveMessageCommand,
  SQSClient,
} from "@aws-sdk/client-sqs";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { StubExtractor } from "./extractor.js";
import { processEmail } from "./handler.js";
import { sendBatch } from "./sqs.js";
import { EventMessageSchema, type EventDraft } from "./schema.js";

const dir = fileURLToPath(new URL("./__fixtures__/", import.meta.url));
const ENDPOINT = process.env.SQS_ENDPOINT ?? "http://localhost:9324";
const sqs = new SQSClient({
  region: "us-east-1",
  endpoint: ENDPOINT,
  credentials: { accessKeyId: "local", secretAccessKey: "local" },
});

const draft: EventDraft = {
  title: "Phoebe Bridgers",
  startsAt: "2026-01-02T20:00:00-05:00",
  venue: { name: "The Bowl", address: "100 Main St" },
  performers: ["Phoebe Bridgers"],
  genres: ["indie"],
};

describe("handler e2e (ElasticMQ)", () => {
  let queueUrl: string;
  beforeAll(async () => {
    const r = await sqs.send(new CreateQueueCommand({ QueueName: `e2e-${Date.now()}` }));
    queueUrl = r.QueueUrl!;
  });
  afterAll(async () => {
    if (queueUrl) await sqs.send(new DeleteQueueCommand({ QueueUrl: queueUrl }));
  });

  it("text email -> a valid EventMessage lands on the queue", async () => {
    await processEmail(readFileSync(join(dir, "text-newsletter.eml")), {
      extractor: new StubExtractor([draft]),
      emit: (msgs) => sendBatch(sqs, queueUrl, msgs),
    });

    const out = await sqs.send(
      new ReceiveMessageCommand({ QueueUrl: queueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 2 }),
    );
    expect(out.Messages).toHaveLength(1);
    const body = JSON.parse(out.Messages![0].Body!);
    expect(EventMessageSchema.safeParse(body).success).toBe(true);
    expect(body.source_id).toBe("email_newsletter");
    expect(body.title).toBe("Phoebe Bridgers");
  });
});
