// Test-only routed `fetch`. Lives beside production code for the same reason
// StubPosterSink lives in poster-sink.ts: it is part of the module's contract
// surface, and colocating keeps it in sync with the client that consumes it.

export interface StubRoute {
  match: RegExp;
  /** JSON body. Ignored when `body` is set. */
  json?: unknown;
  /** Raw bytes, for image fetches. */
  body?: Buffer;
  /** Single status for every match. Defaults to 200. */
  status?: number;
  /** Status per successive call, for retry tests. Overrides `status`. */
  statuses?: number[];
  contentType?: string;
}

export interface StubCall {
  url: string;
  /** Header names lower-cased. */
  headers: Record<string, string>;
}

export type StubFetch = typeof globalThis.fetch & { calls: StubCall[] };

// Derived from fetch itself: this project compiles with lib ES2022 + @types/node
// and no DOM lib, so the global `RequestInfo` alias does not exist here.
type FetchInput = Parameters<typeof globalThis.fetch>[0];
type FetchInit = Parameters<typeof globalThis.fetch>[1];

function headersOf(init?: FetchInit): Record<string, string> {
  const out: Record<string, string> = {};
  const h = init?.headers;
  if (!h) return out;
  if (h instanceof Headers) {
    h.forEach((v, k) => (out[k.toLowerCase()] = v));
  } else if (Array.isArray(h)) {
    for (const [k, v] of h) out[k.toLowerCase()] = v;
  } else {
    for (const [k, v] of Object.entries(h)) out[k.toLowerCase()] = String(v);
  }
  return out;
}

export function stubFetch(routes: StubRoute[]): StubFetch {
  const calls: StubCall[] = [];
  const counts = new Map<StubRoute, number>();

  const fn = async (input: FetchInput, init?: FetchInit): Promise<Response> => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    calls.push({ url, headers: headersOf(init) });

    const route = routes.find((r) => r.match.test(url));
    if (!route) throw new Error(`no stub route for ${url}`);

    const n = counts.get(route) ?? 0;
    counts.set(route, n + 1);
    const status = route.statuses
      ? (route.statuses[Math.min(n, route.statuses.length - 1)] ?? 200)
      : (route.status ?? 200);

    if (route.body) {
      return new Response(new Uint8Array(route.body), {
        status,
        headers: { "content-type": route.contentType ?? "image/jpeg" },
      });
    }
    return new Response(JSON.stringify(route.json ?? {}), {
      status,
      headers: { "content-type": route.contentType ?? "application/json" },
    });
  };

  const typed = fn as unknown as StubFetch;
  typed.calls = calls;
  return typed;
}
