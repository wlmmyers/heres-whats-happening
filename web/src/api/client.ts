// API base URL. Empty in dev (so requests like '/me' go to Vite's /api proxy
// — apiFetch prefixes with '/api'). In production, set VITE_API_BASE_URL to
// e.g. 'https://api.example.com'.
const BASE = import.meta.env.VITE_API_BASE_URL ?? '';

let accessToken: string | null = null;

export function setAccessToken(token: string): void {
  accessToken = token;
}

export function clearAccessToken(): void {
  accessToken = null;
}

export function getAccessToken(): string | null {
  return accessToken;
}

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }
}

interface ApiErrorBody {
  error?: { code?: string; message?: string };
}

function fullURL(path: string): string {
  // In dev, BASE is '' and we use the Vite proxy at /api/*.
  // In prod, BASE is the absolute API origin and we hit it directly (no /api prefix).
  if (BASE === '') return `/api${path}`;
  return `${BASE}${path}`;
}

async function rawFetch(path: string, init: RequestInit): Promise<Response> {
  const headers = new Headers(init.headers ?? {});
  if (accessToken) headers.set('Authorization', `Bearer ${accessToken}`);
  if (init.body !== undefined && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }
  return fetch(fullURL(path), {
    ...init,
    headers,
    credentials: 'include', // sends the refresh-token cookie
  });
}

async function refresh(): Promise<boolean> {
  const resp = await rawFetch('/auth/refresh', { method: 'POST' });
  if (!resp.ok) return false;
  const body = (await resp.json()) as { access_token?: string };
  if (!body.access_token) return false;
  accessToken = body.access_token;
  return true;
}

export interface ApiFetchOptions {
  method?: string;
  body?: unknown;
  signal?: AbortSignal;
}

export async function apiFetch<T = unknown>(path: string, opts: ApiFetchOptions = {}): Promise<T> {
  const init: RequestInit = {
    method: opts.method ?? 'GET',
    signal: opts.signal,
  };
  if (opts.body !== undefined) {
    init.body = JSON.stringify(opts.body);
  }

  let resp = await rawFetch(path, init);

  if (resp.status === 401 && !path.endsWith('/auth/refresh')) {
    const refreshed = await refresh();
    if (refreshed) {
      resp = await rawFetch(path, init);
    }
  }

  if (resp.status === 204) {
    return undefined as T;
  }

  if (!resp.ok) {
    let body: ApiErrorBody = {};
    try {
      body = (await resp.json()) as ApiErrorBody;
    } catch {
      // body might be empty / non-JSON; keep defaults
    }
    throw new ApiError(
      resp.status,
      body.error?.code ?? 'unknown',
      body.error?.message ?? resp.statusText,
    );
  }

  return (await resp.json()) as T;
}
