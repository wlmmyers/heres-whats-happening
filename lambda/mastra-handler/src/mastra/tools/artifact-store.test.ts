import { mkdir, mkdtemp, readFile, stat, utimes, writeFile } from 'node:fs/promises';
import { existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { beforeEach, describe, expect, it } from 'vitest';
import { artifactStore, defaultRoot } from './artifact-store.js';

let root: string;
beforeEach(async () => {
  root = await mkdtemp(join(tmpdir(), 'artifact-store-test-'));
});

describe('artifactStore', () => {
  it('writes a file under a run-scoped directory and returns a ref', async () => {
    const store = artifactStore('run-abc', { root });
    const data = Buffer.from([0xff, 0xd8, 0xff, 0xe0]);

    const ref = await store.write('band-1.jpg', data, 'image/jpeg');

    expect(ref.path).toBe(join(root, 'run-abc', 'band-1.jpg'));
    expect(ref.contentType).toBe('image/jpeg');
    expect(ref.bytes).toBe(4); // SIZE, not content
    expect(await readFile(ref.path)).toEqual(data);
  });

  it('round-trips through read', async () => {
    const store = artifactStore('run-abc', { root });
    const data = Buffer.from('<svg/>', 'utf8');
    const ref = await store.write('poster-1.svg', data, 'image/svg+xml');
    expect(await store.read(ref)).toEqual(data);
  });

  it('keeps different runs in different directories', async () => {
    const a = await artifactStore('run-a', { root }).write('x.png', Buffer.from('a'), 'image/png');
    const b = await artifactStore('run-b', { root }).write('x.png', Buffer.from('b'), 'image/png');

    expect(a.path).not.toBe(b.path);
    expect(await readFile(a.path, 'utf8')).toBe('a');
    expect(await readFile(b.path, 'utf8')).toBe('b');
  });

  it('exposes the run directory so callers can clean it up', () => {
    expect(artifactStore('run-abc', { root }).dir).toBe(join(root, 'run-abc'));
  });

  it('sweeps run directories older than an hour, sparing fresh ones', async () => {
    const stale = join(root, 'stale-run');
    const fresh = join(root, 'fresh-run');
    await mkdir(stale, { recursive: true });
    await mkdir(fresh, { recursive: true });
    await writeFile(join(stale, 'f'), 'x');
    await writeFile(join(fresh, 'f'), 'x');
    const old = new Date(Date.now() - 2 * 60 * 60 * 1000);
    await utimes(stale, old, old);

    // The sweep runs lazily on first write.
    await artifactStore('new-run', { root }).write('a.png', Buffer.from('a'), 'image/png');

    expect(existsSync(stale)).toBe(false);
    expect(existsSync(fresh)).toBe(true);
  });

  it('survives a root that does not exist yet', async () => {
    const missing = join(root, 'nested', 'deeper');
    const ref = await artifactStore('run-abc', { root: missing }).write(
      'a.png',
      Buffer.from('a'),
      'image/png',
    );
    expect((await stat(ref.path)).size).toBe(1);
  });
});

describe('defaultRoot', () => {
  it('lives under the OS temp dir', () => {
    expect(defaultRoot().startsWith(tmpdir())).toBe(true);
    expect(defaultRoot()).toContain('hwh-poster');
  });
});
