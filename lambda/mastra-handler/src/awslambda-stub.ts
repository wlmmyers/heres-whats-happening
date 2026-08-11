/**
 * `handler.ts` calls `awslambda.streamifyResponse(...)` at module load. That global
 * is injected by the Lambda runtime, so importing the module anywhere else — tests,
 * local scripts — throws `awslambda.streamifyResponse is not a function` before a
 * single line of your own code runs.
 *
 * Call this BEFORE importing anything that pulls in `handler.ts`. Because ESM
 * hoists static imports, a caller that needs the stub first must import the
 * handler dynamically (`await import(...)`).
 */
export function stubAwsLambdaGlobal(): void {
  (globalThis as Record<string, unknown>).awslambda = {
    streamifyResponse: (fn: unknown) => fn,
    HttpResponseStream: {
      from: (stream: unknown, _meta: unknown) => stream,
    },
  };
}
