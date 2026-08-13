import { Agent } from '@mastra/core/agent';
import { toStandardSchema } from '@mastra/core/schema';
import { z } from 'zod';

export const TourBlurbSchema = z.object({
  blurb: z
    .string()
    .describe('One or two sentences on what this band has been playing live lately.'),
  usable: z
    .boolean()
    .describe('False when the supplied setlist data is too thin to say anything concrete.'),
});

export const tourBlurbAgent = new Agent({
  id: 'artist-tour-blurb',
  name: 'Artist Tour Blurb Writer',
  instructions: `You write a one or two sentence note about what a band has been playing live.

You are given: a tour name (sometimes), the date, venue and city of one recent
show, the number of songs they played, and the first few song titles.

Write one or two sentences a fan would find useful before buying a ticket — what
they have been playing lately, and the tour name if there is one.

Rules you must not break:
- Use ONLY the facts supplied. Invent no dates, no venues, no song titles, no
  album references, no band history.
- Do not claim the band WILL play any particular song. The data is one past
  show, not a promise.
- If there is no tour name and fewer than three songs, set usable to false and
  return an empty blurb.`,
  model: process.env.LLM_MODEL || 'anthropic/claude-sonnet-4-5',
  defaultOptions: { structuredOutput: { schema: toStandardSchema(TourBlurbSchema) } },
});
