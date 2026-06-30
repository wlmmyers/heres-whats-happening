import { SendMessageBatchCommand, type SQSClient } from "@aws-sdk/client-sqs";
import type { EventMessage } from "./schema.js";

const MAX_BATCH = 10; // SQS SendMessageBatch hard limit

/** Send messages to the events-queue in batches of <=10. Throws if any entry
 * fails after the batch call (the caller fails the invocation -> retry/DLQ;
 * safe because source_event_id is a deterministic hash + the consumer upserts). */
export async function sendBatch(
  sqs: SQSClient,
  queueUrl: string,
  messages: EventMessage[],
): Promise<void> {
  for (let i = 0; i < messages.length; i += MAX_BATCH) {
    const chunk = messages.slice(i, i + MAX_BATCH);
    const out = await sqs.send(
      new SendMessageBatchCommand({
        QueueUrl: queueUrl,
        Entries: chunk.map((m, j) => ({
          Id: String(i + j),
          MessageBody: JSON.stringify(m),
        })),
      }),
    );
    if (out.Failed && out.Failed.length > 0) {
      throw new Error(`SendMessageBatch failed for ${out.Failed.length} entr(ies): ${JSON.stringify(out.Failed)}`);
    }
  }
}
