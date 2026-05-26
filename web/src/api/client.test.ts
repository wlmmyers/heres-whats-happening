import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { apiFetch, setAccessToken, clearAccessToken, ApiError } from './client';

const realFetch = global.fetch;

beforeEach(() => {
  clearAccessToken();
});

afterEach(() => {
  global.fetch = realFetch;
  vi.restoreAllMocks();
});

function mockJsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('apiFetch', () => {
  it('returns parsed JSON on success', async () => {
    global.fetch = vi.fn().mockResolvedValueOnce(mockJsonResponse(200, { hello: 'world' }));
    const out = await apiFetch<{ hello: string }>('/me');
    expect(out).toEqual({ hello: 'world' });
  });

  it('sends Authorization header when access token is set', async () => {
    setAccessToken('access-1');
    const spy = vi.fn().mockResolvedValueOnce(mockJsonResponse(200, {}));
    global.fetch = spy;
    await apiFetch('/me');
    const headers = spy.mock.calls[0][1].headers as Headers;
    expect(headers.get('Authorization')).toBe('Bearer access-1');
  });

  it('refreshes on 401 then retries once', async () => {
    setAccessToken('expired');
    let call = 0;
    global.fetch = vi.fn().mockImplementation((url: string) => {
      call += 1;
      if (call === 1) return Promise.resolve(mockJsonResponse(401, { error: { code: 'invalid_token' } }));
      if (call === 2 && url.endsWith('/auth/refresh')) {
        return Promise.resolve(mockJsonResponse(200, { access_token: 'fresh' }));
      }
      return Promise.resolve(mockJsonResponse(200, { ok: true }));
    });

    const out = await apiFetch<{ ok: boolean }>('/me');
    expect(out).toEqual({ ok: true });
    expect(call).toBe(3);
  });

  it('throws ApiError with status + code on non-2xx (not 401)', async () => {
    global.fetch = vi.fn().mockResolvedValueOnce(
      mockJsonResponse(400, { error: { code: 'bad_request', message: 'nope' } }),
    );
    await expect(apiFetch('/me')).rejects.toThrowError(
      expect.objectContaining({ status: 400, code: 'bad_request' }),
    );
  });

  it('throws when refresh itself fails', async () => {
    setAccessToken('expired');
    global.fetch = vi.fn().mockImplementation((url: string) => {
      if (url.endsWith('/auth/refresh')) {
        return Promise.resolve(mockJsonResponse(401, { error: { code: 'no_refresh' } }));
      }
      return Promise.resolve(mockJsonResponse(401, { error: { code: 'invalid_token' } }));
    });

    await expect(apiFetch('/me')).rejects.toThrowError(ApiError);
  });
});
