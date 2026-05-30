import { GetSecretValueCommand, SecretsManagerClient } from "@aws-sdk/client-secrets-manager";
import type { EmailImage } from "./email.js";
import { emailExtractorAgent } from "./mastra/agents/email-extractor.agent.js";
import { EventDraftsSchema, type EventDraft } from "./schema.js";

export interface ExtractInput {
  mode: "text" | "image";
  text: string;
  images: EmailImage[];
  receivedAt?: string; // Date header — injected for relative-date year resolution
}

export interface EventExtractor {
  extract(input: ExtractInput): Promise<EventDraft[]>;
}

/** Test double. Returns canned drafts; records inputs for assertions. */
export class StubExtractor implements EventExtractor {
  public calls: ExtractInput[] = [];
  constructor(private readonly drafts: EventDraft[]) {}
  async extract(input: ExtractInput): Promise<EventDraft[]> {
    this.calls.push(input);
    return this.drafts;
  }
}

/** Minimal seam over Secrets Manager so the loader is unit-testable. */
export interface SecretReader {
  getSecretValue(arn: string): Promise<string>;
}

/** Cold-start: populate ANTHROPIC_API_KEY from Secrets Manager if not already set
 * (the Mastra model router reads ANTHROPIC_API_KEY at generate() time). Idempotent. */
export async function loadModelKey(reader: SecretReader, secretArn: string): Promise<void> {
  if (process.env.ANTHROPIC_API_KEY) return;
  process.env.ANTHROPIC_API_KEY = await reader.getSecretValue(secretArn);
}

export class AwsSecretReader implements SecretReader {
  private readonly client: SecretsManagerClient;
  constructor(region?: string) {
    this.client = new SecretsManagerClient({ region });
  }
  async getSecretValue(arn: string): Promise<string> {
    const out = await this.client.send(new GetSecretValueCommand({ SecretId: arn }));
    if (!out.SecretString) throw new Error(`secret ${arn} has no string value`);
    return out.SecretString;
  }
}

/** Real extractor — wraps the registered Mastra agent. Not unit-tested (needs
 * ANTHROPIC_API_KEY); exercised via Studio (`pnpm dev`) and the invoke-local harness. */
export class MastraExtractor implements EventExtractor {
  async extract(input: ExtractInput): Promise<EventDraft[]> {
    const dateLine = input.receivedAt
      ? `This email was received on ${input.receivedAt}. Use it to resolve the correct year for relative dates such as "this Friday".`
      : "";
    const content: Array<
      | { type: "text"; text: string }
      | { type: "image"; image: Buffer; mimeType: string }
    > =
      input.mode === "image"
        ? [
            { type: "text" as const, text: `${dateLine}\nExtract the events shown in the attached flyer image(s).` },
            ...input.images.map((img) => ({ type: "image" as const, image: img.data, mimeType: img.contentType })),
          ]
        : [{ type: "text" as const, text: `${dateLine}\n\n${input.text}` }];

    const res = await emailExtractorAgent.generate(
      [{ role: "user", content }],
      { structuredOutput: { schema: EventDraftsSchema } },
    );
    return (res.object?.events ?? []) as EventDraft[];
  }
}
