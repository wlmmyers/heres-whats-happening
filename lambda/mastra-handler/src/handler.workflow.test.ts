import { existsSync } from 'node:fs';
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { StubPosterSink } from './poster-sink.js';
import { processPosterRequest } from './poster.js';

const RUN_ID = 'run-workflow-test';
const start = vi.fn();

/** Assigned in beforeEach; the mock below reads it at CALL time, not import time. */
let root: string;

// Stand in for the real workflow so a provider outage can be simulated without a
// network call. `createRun` hands back the same runId the steps would see, which
// is exactly what makes the artifact directory derivable.
vi.mock('./mastra/workflows/poster.workflow.js', () => ({
  posterWorkflow: { createRun: async () => ({ runId: RUN_ID, start }) },
}));
// Redirect the scratch root at the module seam so the test writes under its own
// temp dir. handler.ts and the steps import the SAME module, so one mock covers both.
vi.mock('./mastra/tools/artifact-store.js', async () => {
  const actual = await vi.importActual<typeof import('./mastra/tools/artifact-store.js')>(
    './mastra/tools/artifact-store.js',
  );
  return { ...actual, artifactStore: (runId: string) => actual.artifactStore(runId, { root }) };
});

const { runPosterWorkflow } = await import('./handler.js');
const { artifactStore } = await import('./mastra/tools/artifact-store.js');

const req = {
  userId: '550e8400-e29b-41d4-a716-446655440000',
  performer: 'Khruangbin',
  venue: 'The Fillmore',
  date: '2026-08-15',
  force: false,
};

const success = {
  status: 'success',
  result: {
    ok: true,
    render: {
      svg: { path: '/tmp/x/p.svg', contentType: 'image/svg+xml', bytes: 10 },
      png: { path: '/tmp/x/p.png', contentType: 'image/png', bytes: 20 },
    },
    artifactDir: '/tmp/x',
  },
};

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), 'handler-workflow-test-'));
  start.mockReset();
});

describe('runPosterWorkflow', () => {
  it("passes a successful run's output through untouched", async () => {
    start.mockResolvedValue(success);
    expect(await runPosterWorkflow(req)).toEqual(success.result);
  });

  it('returns a controlled failure — WITH artifactDir — when the run THROWS', async () => {
    start.mockRejectedValue(new Error('Overloaded: 529'));

    const out = await runPosterWorkflow(req);

    expect(out.ok).toBe(false);
    expect(out.reason).toContain('529');
    // Without this the caller's `finally` sees no artifactDir and skips cleanup.
    expect(out.artifactDir).toBe(artifactStore(RUN_ID).dir);
  });

  it('returns a controlled failure — WITH artifactDir — when the run ends non-success', async () => {
    start.mockResolvedValue({ status: 'failed', error: { message: 'step exploded' } });

    const out = await runPosterWorkflow(req);

    expect(out.ok).toBe(false);
    expect(out.reason).toContain('step exploded');
    expect(out.artifactDir).toBe(artifactStore(RUN_ID).dir);
  });

  it("names the directory the run's own steps write into", async () => {
    // Proves the derivation is not merely self-consistent: a step writing through
    // artifactStore(runId) lands INSIDE the directory this function reports.
    start.mockImplementation(async () => {
      await artifactStore(RUN_ID).write('band-1.jpg', Buffer.from([0xff, 0xd8]), 'image/jpeg');
      throw new Error('Overloaded: 529');
    });

    const out = await runPosterWorkflow(req);

    expect(existsSync(join(out.artifactDir!, 'band-1.jpg'))).toBe(true);
  });
});

describe('scratch cleanup after a dead workflow', () => {
  it('deletes the run directory instead of leaking it on /tmp', async () => {
    // The reproduced leak: the step wrote band-1.jpg, the throw escaped, `out` was
    // never assigned, `out?.artifactDir` was undefined, and the file survived on
    // Lambda's persistent /tmp across warm invocations.
    start.mockImplementation(async () => {
      await artifactStore(RUN_ID).write('band-1.jpg', Buffer.from([0xff, 0xd8]), 'image/jpeg');
      throw new Error('Overloaded: 529');
    });
    const sink = new StubPosterSink();

    const res = await processPosterRequest(req, { sink, runWorkflow: runPosterWorkflow });

    expect(res.ok).toBe(false);
    if (!res.ok) expect(res.reason).toContain('529');
    expect(sink.calls).toHaveLength(0); // nothing half-baked was published
    expect(existsSync(artifactStore(RUN_ID).dir)).toBe(false);
  });
});
