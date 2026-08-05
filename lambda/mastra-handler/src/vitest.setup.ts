// Stub the awslambda streaming global so handler.ts can be imported in tests.
// In production, this global is injected by the Lambda runtime.
(globalThis as Record<string, unknown>).awslambda = {
  streamifyResponse: (fn: unknown) => fn,
  HttpResponseStream: {
    from: (stream: unknown, _meta: unknown) => stream,
  },
};
