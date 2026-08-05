import type { Writable } from "node:stream";

declare global {
  // eslint-disable-next-line @typescript-eslint/no-namespace
  namespace awslambda {
    interface ResponseStream extends Writable {
      setContentType(contentType: string): void;
    }
    interface HttpResponseStreamMeta {
      statusCode?: number;
      headers?: Record<string, string>;
    }
    const HttpResponseStream: {
      from(stream: ResponseStream, meta: HttpResponseStreamMeta): ResponseStream;
    };
    function streamifyResponse<E = unknown>(
      handler: (event: E, responseStream: ResponseStream, context: unknown) => Promise<void>,
    ): (event: E, responseStream: ResponseStream, context: unknown) => Promise<void>;
  }
}

export {};
