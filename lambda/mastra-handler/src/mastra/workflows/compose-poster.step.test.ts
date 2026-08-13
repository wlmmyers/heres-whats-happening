import { mkdtemp, readdir, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { RasterizeResult } from '../tools/rasterize.tool.js';

const authorGenerate = vi.fn();
const critiqueGenerate = vi.fn();
const rasterizeSvg = vi.fn<(svg: string) => Promise<RasterizeResult>>();

/** Assigned in beforeEach; the mock below reads it at CALL time, not import time. */
let root: string;

// vi.mock is hoisted, but the factory body runs lazily at import time — by which
// point the spies above are initialized. So they can be referenced directly.
// svg-parse.tool.js is deliberately NOT mocked: it is pure and already covered,
// so the real substitution/validation runs here.
vi.mock('../agents/svg-author.agent.js', () => ({
  svgAuthorAgent: { generate: authorGenerate },
  SvgAuthorSchema: {},
}));
vi.mock('../agents/poster-critique.agent.js', () => ({
  posterCritiqueAgent: { generate: critiqueGenerate },
  PosterCritiqueSchema: {},
}));
vi.mock('../tools/rasterize.tool.js', () => ({ rasterizeSvg }));
// The step calls artifactStore(runId) with the default root. Redirect it at the
// module seam, the same way this file already mocks the agents and rasterizer,
// so production needs no test-only parameter.
vi.mock('../tools/artifact-store.js', async () => {
  const actual = await vi.importActual<typeof import('../tools/artifact-store.js')>(
    '../tools/artifact-store.js',
  );
  return { ...actual, artifactStore: (runId: string) => actual.artifactStore(runId, { root }) };
});

const { composePosterStep } = await import('./compose-poster.step.js');

const GOOD_SVG = '<svg xmlns="http://www.w3.org/2000/svg"><image href="__BAND_IMAGE__"/></svg>';

const base = {
  performer: 'La Luz',
  venue: 'Occidental Square',
  date: 'Thursday, August 20',
  imageOk: true,
  colors: ['#111'],
  attempts: 0,
  accepted: false,
  image: { path: '', contentType: 'image/jpeg', bytes: 4, width: 1080, height: 810 },
};

const PHOTO = Buffer.from([0xff, 0xd8, 0xff, 0xe0]);
const PNG = Buffer.from([0x89, 0x50, 0x4e, 0x47]);

const run = (data: Record<string, unknown>) =>
  composePosterStep.execute({ inputData: data, runId: 'run-test' } as never) as Promise<any>;

beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), 'compose-step-test-'));
  authorGenerate.mockReset();
  critiqueGenerate.mockReset();
  rasterizeSvg.mockReset();
  // Give the step a real band-photo file to read. This import returns the MOCKED
  // artifactStore, which is already rooted at `root`.
  const { artifactStore } = await import('../tools/artifact-store.js');
  const ref = await artifactStore('run-test').write('band-1.jpg', PHOTO, 'image/jpeg');
  base.image = { ...ref, width: 1080, height: 810 };
});

describe('composePosterStep short-circuit', () => {
  it('does no LLM work when the image stage failed', async () => {
    const out = await run({ ...base, imageOk: false, image: undefined });

    expect(authorGenerate).not.toHaveBeenCalled();
    expect(critiqueGenerate).not.toHaveBeenCalled();
    expect(rasterizeSvg).not.toHaveBeenCalled();
    expect(out.accepted).toBe(false);
  });

  it('ADVANCES attempts on the short-circuit so the loop cannot spin', async () => {
    // Regression guard: the dountil exit is
    // `accepted || !imageOk || attempts >= MAX_SVG_ATTEMPTS`. If this branch ever
    // returns without advancing attempts, an imageOk/image mismatch loops forever.
    const out = await run({ ...base, imageOk: true, image: undefined });

    expect(out.attempts).toBe(1);
    expect(out.accepted).toBe(false);
    expect(out.critique).toContain('no usable band image');
    expect(authorGenerate).not.toHaveBeenCalled();
  });

  it('keeps an existing critique rather than overwriting it', async () => {
    const out = await run({
      ...base,
      imageOk: false,
      image: undefined,
      critique: 'earlier feedback',
    });
    expect(out.critique).toBe('earlier feedback');
  });
});

describe('composePosterStep authoring', () => {
  it('writes ONLY a png, and keeps no svg in state', async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, png: PNG, width: 1080, height: 1350 });
    critiqueGenerate.mockResolvedValue({
      object: { acceptable: true, critique: 'bold and legible' },
    });

    const out = await run(base);

    expect(out.accepted).toBe(true);
    expect(out.render.png.path).toContain(join('run-test', 'poster-1.png'));
    expect(out.render.png.bytes).toBe(PNG.byteLength);
    expect(await readFile(out.render.png.path)).toEqual(PNG);

    // The SVG is a transient on the way to the PNG — nothing persists it.
    expect('svg' in out.render).toBe(false);
    expect('authoredSvg' in out).toBe(false);
    const files = await readdir(join(root, 'run-test'));
    expect(files.filter((f) => f.endsWith('.svg'))).toEqual([]);
  });

  it('rejects and records the critique when the poster is judged poor', async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, png: PNG });
    critiqueGenerate.mockResolvedValue({
      object: { acceptable: false, critique: 'title is unreadable' },
    });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toBe('title is unreadable');
    expect(out.attempts).toBe(1);
  });

  it('feeds the prior critique back to the author', async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, png: PNG });
    critiqueGenerate.mockResolvedValue({ object: { acceptable: true, critique: 'ok' } });

    await run({ ...base, attempts: 1, critique: 'the date was cropped' });

    const payload = JSON.parse(
      (authorGenerate.mock.calls[0][0] as Array<{ content: string }>)[0].content,
    );
    expect(payload.critique).toBe('the date was cropped');
    expect(payload.imageWidth).toBe(1080);
    expect(payload.imageHeight).toBe(810);
    expect(payload.colors).toEqual(['#111']);
  });
});

describe('composePosterStep failure paths', () => {
  it('produces a critique when the band photo file cannot be read', async () => {
    const out = await run({ ...base, image: { ...base.image, path: join(root, 'gone.jpg') } });

    expect(out.accepted).toBe(false);
    expect(out.attempts).toBe(1);
    expect(out.critique).toContain('could not read the band image');
    expect(authorGenerate).not.toHaveBeenCalled();
  });

  it('handles the author returning no svg', async () => {
    authorGenerate.mockResolvedValue({ object: undefined });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.attempts).toBe(1);
    expect(out.critique).toContain('no svg');
    expect(rasterizeSvg).not.toHaveBeenCalled();
  });

  it('reports a missing image placeholder as actionable feedback', async () => {
    authorGenerate.mockResolvedValue({
      object: { svg: '<svg xmlns="http://www.w3.org/2000/svg"></svg>' },
    });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toContain('__BAND_IMAGE__');
    expect(rasterizeSvg).not.toHaveBeenCalled();
  });

  it('reports malformed svg without attempting to rasterize', async () => {
    authorGenerate.mockResolvedValue({
      object: { svg: '<svg><image href="__BAND_IMAGE__"></svg>' },
    });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toContain('well-formed');
    expect(rasterizeSvg).not.toHaveBeenCalled();
  });

  it("reports a rasterization failure with the renderer's error", async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: false, error: 'unsupported filter' });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toContain('did not render');
    expect(out.critique).toContain('unsupported filter');
    expect(critiqueGenerate).not.toHaveBeenCalled();
  });

  it('converts a THROWN author call into returned state (never throws out of the step)', async () => {
    // An Anthropic 529/5xx used to escape the step: 500 to the caller instead of a
    // controlled 422, and the run's scratch files leaked because cleanup keys off
    // the workflow's returned output.
    authorGenerate.mockRejectedValue(new Error('Overloaded: 529'));

    const out = await run(base);

    expect(out.accepted).toBe(false);
    expect(out.attempts).toBe(1);
    expect(out.critique).toContain('SVG author failed');
    expect(out.critique).toContain('529');
    expect(rasterizeSvg).not.toHaveBeenCalled();
  });

  it('converts a THROWN critique call into returned state, keeping the render it made', async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, png: PNG });
    critiqueGenerate.mockRejectedValue(new Error('anthropic timeout'));

    const out = await run(base);

    expect(out.accepted).toBe(false);
    expect(out.attempts).toBe(1);
    expect(out.critique).toContain('poster critique failed');
    expect(out.critique).toContain('anthropic timeout');
    expect(out.render.png.path).toContain('poster-1.png');
  });

  it('handles the critique agent returning no structured object', async () => {
    authorGenerate.mockResolvedValue({ object: { svg: GOOD_SVG } });
    rasterizeSvg.mockResolvedValue({ ok: true, png: PNG });
    critiqueGenerate.mockResolvedValue({ object: undefined });

    const out = await run(base);
    expect(out.accepted).toBe(false);
    expect(out.critique).toBe('critique returned no result');
  });
});
