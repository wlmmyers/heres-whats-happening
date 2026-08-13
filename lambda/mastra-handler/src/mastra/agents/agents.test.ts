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
    expect(await instructionsOf(bioAuthorAgent)).toMatch(/NOTHING about current or upcoming tours/);
  });

  it('makes the album list authoritative over the prose', async () => {
    expect(await instructionsOf(bioAuthorAgent)).toMatch(/album list wins/);
  });
});

describe('tourBlurbAgent', () => {
  it('forbids inventing facts', async () => {
    expect(await instructionsOf(tourBlurbAgent)).toMatch(/Invent no dates/);
  });

  it('forbids promising a future setlist', async () => {
    expect(await instructionsOf(tourBlurbAgent)).toMatch(/not a promise/);
  });
});
