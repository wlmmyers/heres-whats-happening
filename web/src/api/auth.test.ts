import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { deleteAccount } from './auth';
import { clearAccessToken, getAccessToken, setAccessToken } from './client';

const realFetch = global.fetch;

beforeEach(() => {
  clearAccessToken();
});

afterEach(() => {
  global.fetch = realFetch;
  vi.restoreAllMocks();
});

describe('deleteAccount', () => {
  it('sends DELETE /me and forgets the access token', async () => {
    setAccessToken('access-1');
    const spy = vi.fn().mockResolvedValueOnce(new Response(null, { status: 204 }));
    global.fetch = spy;

    await deleteAccount();

    const [url, init] = spy.mock.calls[0] as [string, RequestInit];
    expect(url).toMatch(/\/me$/);
    expect(init.method).toBe('DELETE');
    expect(getAccessToken()).toBeNull();
  });

  it('keeps the access token when the delete fails, since the account still exists', async () => {
    setAccessToken('access-1');
    global.fetch = vi.fn().mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'db_error', message: 'nope' } }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }),
    );

    await expect(deleteAccount()).rejects.toMatchObject({ status: 500 });
    expect(getAccessToken()).toBe('access-1');
  });
});
