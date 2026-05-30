import { simpleParser } from "mailparser";

export const TEXT_MIN_CHARS = 40;

export interface EmailImage {
  contentType: string;
  data: Buffer;
}

export interface ParsedEmail {
  spamFail: boolean;
  virusFail: boolean;
  date?: string; // RFC string of the Date header, for LLM year context
  text: string;
  images: EmailImage[];
}

function verdictFails(value: string | undefined): boolean {
  return (value ?? "").trim().toUpperCase() === "FAIL";
}

export async function parseEmail(raw: Buffer): Promise<ParsedEmail> {
  const parsed = await simpleParser(raw);
  const spam = parsed.headers.get("x-ses-spam-verdict") as string | undefined;
  const virus = parsed.headers.get("x-ses-virus-verdict") as string | undefined;

  const images: EmailImage[] = (parsed.attachments ?? [])
    .filter((a) => a.contentType?.startsWith("image/"))
    .map((a) => ({ contentType: a.contentType, data: a.content }));

  return {
    spamFail: verdictFails(spam),
    virusFail: verdictFails(virus),
    date: parsed.date ? parsed.date.toUTCString() : undefined,
    text: (parsed.text ?? "").trim(),
    images,
  };
}

export type GateDecision = "skip" | "text" | "image";

/** Decide how (or whether) to parse: drop spam/virus, prefer the text body,
 * fall back to images only when the body is too thin to parse. */
export function gate(p: ParsedEmail): GateDecision {
  if (p.spamFail || p.virusFail) return "skip";
  if (p.text.length >= TEXT_MIN_CHARS) return "text";
  if (p.images.length > 0) return "image";
  return "skip";
}
