import {
  CreateQueueCommand,
  DeleteQueueCommand,
  ReceiveMessageCommand,
  SQSClient,
} from "@aws-sdk/client-sqs";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { sendBatch } from "./sqs.js";
import type { EventMessage } from "./schema.js";

const ENDPOINT = process.env.SQS_ENDPOINT ?? "http://localhost:9324";

function client() {
  return new SQSClient({
    region: "us-east-1",
    endpoint: ENDPOINT,
    credentials: { accessKeyId: "local", secretAccessKey: "local" },
  });
}

function msg(i: number): EventMessage {
  return {
    source_id: "email_newsletter",
    source_event_id: `hash-${i}`,
    title: `Show ${i}`,
    starts_at: "2026-06-15T20:00:00Z",
    venue: { name: "The Bowl" },
  };
}

describe("sendBatch (ElasticMQ)", () => {
  const sqs = client();
  let queueUrl: string;

  beforeAll(async () => {
    const r = await sqs.send(new CreateQueueCommand({ QueueName: `sqs-test-${Date.now()}` }));
    queueUrl = r.QueueUrl!;
  });
  afterAll(async () => {
    if (queueUrl) await sqs.send(new DeleteQueueCommand({ QueueUrl: queueUrl }));
  });

  it("chunks >10 messages and delivers all of them", async () => {
    const messages = Array.from({ length: 23 }, (_, i) => msg(i));
    await sendBatch(sqs, queueUrl, messages);

    const received = new Set<string>();
    for (let i = 0; i < 6 && received.size < 23; i++) {
      const out = await sqs.send(
        new ReceiveMessageCommand({ QueueUrl: queueUrl, MaxNumberOfMessages: 10, WaitTimeSeconds: 1 }),
      );
      for (const m of out.Messages ?? []) {
        received.add(JSON.parse(m.Body!).source_event_id);
      }
    }
    expect(received.size).toBe(23);
  });
});
