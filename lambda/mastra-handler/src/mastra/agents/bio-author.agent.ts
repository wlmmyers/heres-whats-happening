import { Agent } from '@mastra/core/agent';
import { toStandardSchema } from '@mastra/core/schema';
import { z } from 'zod';

export const BioOutputSchema = z.object({
  bio: z
    .string()
    .describe(
      "150-250 words of plain markdown covering the band's origins and notable releases. No headings.",
    ),
  usable: z
    .boolean()
    .describe(
      'False when the supplied source text is too thin to write an accurate bio. Prefer false over inventing.',
    ),
});

export const bioAuthorAgent = new Agent({
  id: 'artist-bio-author',
  name: 'Artist Bio Author',
  instructions: `You write short, factual artist bios for a concert listings site.

You are given an artist name, an extract from their English Wikipedia article,
and a list of their albums with release years taken from MusicBrainz.

Write 150-250 words of plain markdown covering, in this order: where and when the
band formed and who is in it; their notable releases; and what they are known for
musically. No headings, no bullet lists, no preamble — just prose.

Rules you must not break:
- Use ONLY facts present in the supplied extract and album list. Invent nothing.
- The album list is authoritative for titles and years. If the extract disagrees
  with it, the album list wins.
- Write NOTHING about current or upcoming tours, dates, or venues. That
  information is not in your input and is handled elsewhere.
- If the extract is too thin to write an accurate bio, set usable to false and
  return an empty bio rather than padding it out.`,
  model: process.env.LLM_MODEL || 'anthropic/claude-sonnet-4-5',
  defaultOptions: { structuredOutput: { schema: toStandardSchema(BioOutputSchema) } },
});
