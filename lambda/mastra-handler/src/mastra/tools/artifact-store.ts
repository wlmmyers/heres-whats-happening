import { mkdir, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import type { ArtifactRef } from './band-image.js';

const ROOT_NAME = 'hwh-poster';
const SWEEP_MAX_AGE_MS = 60 * 60 * 1000;

/** Roots already swept in this process — the sweep is a backstop, not per-call work. */
const sweptRoots = new Set<string>();

export interface ArtifactStore {
  /** The run-scoped directory. Callers delete this to clean up a whole run. */
  readonly dir: string;
  write(name: string, data: Buffer, contentType: string): Promise<ArtifactRef>;
  read(ref: { path: string }): Promise<Buffer>;
}

export function defaultRoot(): string {
  return join(tmpdir(), ROOT_NAME);
}

/**
 * Delete run directories older than an hour. Best-effort: a local dev loop
 * should not fill tmpdir, but a failed sweep must never fail a request.
 */
async function sweep(root: string): Promise<void> {
  if (sweptRoots.has(root)) return;
  sweptRoots.add(root);
  try {
    const entries = await readdir(root, { withFileTypes: true });
    const cutoff = Date.now() - SWEEP_MAX_AGE_MS;
    await Promise.all(
      entries
        .filter((e) => e.isDirectory())
        .map(async (e) => {
          const dir = join(root, e.name);
          try {
            const info = await stat(dir);
            if (info.mtimeMs < cutoff) await rm(dir, { recursive: true, force: true });
          } catch {
            // Raced with another sweep, or vanished. Nothing to do.
          }
        }),
    );
  } catch {
    // Root does not exist yet. Nothing to sweep.
  }
}

/**
 * Files for one workflow run. `runId` comes from the step's execute params, so
 * every artifact of a run lands together and Studio traces point at real paths.
 */
export function artifactStore(runId: string, opts: { root?: string } = {}): ArtifactStore {
  const root = opts.root ?? defaultRoot();
  const dir = join(root, runId);
  let ready: Promise<void> | undefined;

  const ensure = (): Promise<void> =>
    (ready ??= (async () => {
      await sweep(root);
      await mkdir(dir, { recursive: true });
    })());

  return {
    dir,
    async write(name, data, contentType) {
      await ensure();
      const path = join(dir, name);
      await writeFile(path, data);
      return { path, contentType, bytes: data.byteLength };
    },
    async read(ref) {
      return readFile(ref.path);
    },
  };
}
