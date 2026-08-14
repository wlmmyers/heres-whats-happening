import { describe, expect, it } from 'vitest';
import { bioAuthorAgent } from './bio-author.agent.js';
import { tourBlurbAgent } from './tour-blurb.agent.js';

// Agent.getInstructions() is async in @mastra/core (see
// node_modules/@mastra/core/dist/agent/agent.d.ts:584).
async function instructionsOf(agent: { getInstructions(): unknown }): Promise<string> {
  return String(await agent.getInstructions());
}

describe('bioAuthorAgent', () => {
  it('forbids tour claims — that grounding lives in the tour workflow', async () => {
    expect(await instructionsOf(bioAuthorAgent)).toMatch(
      /NOTHING about current or upcoming tours, dates, or venues/,
    );
  });

  it('makes the album list authoritative over the prose', async () => {
    expect(await instructionsOf(bioAuthorAgent)).toMatch(
      /The album list is authoritative for titles and years\. If the extract disagrees\s+with it, the album list wins/,
    );
  });

  it('returns usable: false and empty bio when source is too thin', async () => {
    expect(await instructionsOf(bioAuthorAgent)).toMatch(
      /If the extract is too thin to write an accurate bio, set usable to false and\s+return an empty bio rather than padding it out/,
    );
  });
});

describe('tourBlurbAgent', () => {
  it('forbids inventing facts across all categories', async () => {
    expect(await instructionsOf(tourBlurbAgent)).toMatch(
      /Invent no dates, no venues, no song titles, no\s+album references, no band history/,
    );
  });

  it('forbids promising a future setlist', async () => {
    expect(await instructionsOf(tourBlurbAgent)).toMatch(
      /The data is one past\s+show, not a promise/,
    );
  });

  it('returns usable: false and empty blurb when insufficient songs or no tour name', async () => {
    expect(await instructionsOf(tourBlurbAgent)).toMatch(
      /If there is no tour name and fewer than three songs, set usable to false and\s+return an empty blurb/,
    );
  });
});
