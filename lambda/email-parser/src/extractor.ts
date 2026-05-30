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

/** Real extractor — wraps the registered Mastra agent. Not unit-tested (needs
 * ANTHROPIC_API_KEY); exercised via Studio (`pnpm dev`) and the invoke-local harness. */
export class MastraExtractor implements EventExtractor {
  async extract(input: ExtractInput): Promise<EventDraft[]> {
    const dateLine = input.receivedAt
      ? `This email was received on ${input.receivedAt}. Use it to resolve the correct year for relative dates such as "this Friday".`
      : "";
    const content =
      input.mode === "image"
        ? [
            { type: "text", text: `${dateLine}\nExtract the events shown in the attached flyer image(s).` },
            ...input.images.map((img) => ({ type: "image", image: img.data, mimeType: img.contentType })),
          ]
        : [{ type: "text", text: `${dateLine}\n\n${input.text}` }];

    const res = await emailExtractorAgent.generate(
      [{ role: "user", content: content as never }],
      { structuredOutput: { schema: EventDraftsSchema } },
    );
    return (res.object?.events ?? []) as EventDraft[];
  }
}
