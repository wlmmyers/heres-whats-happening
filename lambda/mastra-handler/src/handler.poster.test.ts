import { describe, expect, it } from "vitest";
import type { APIGatewayProxyEventV2, S3Event } from "aws-lambda";
import { StubPosterSink } from "./poster-sink.js";
import { handlePosterHttp, isFunctionUrlEvent } from "./handler.js";

function fnUrlEvent(body: unknown): APIGatewayProxyEventV2 {
  return {
    version: "2.0",
    routeKey: "$default",
    rawPath: "/api/poster",
    rawQueryString: "",
    headers: { "content-type": "application/json" },
    requestContext: { http: { method: "POST", path: "/api/poster" } },
    body: JSON.stringify(body),
    isBase64Encoded: false,
  } as unknown as APIGatewayProxyEventV2;
}

const s3Event = { Records: [{ eventSource: "aws:s3", s3: { bucket: { name: "b" }, object: { key: "raw/x" } } }] } as unknown as S3Event;

describe("isFunctionUrlEvent", () => {
  it("is true for a v2 Function URL event", () => {
    expect(isFunctionUrlEvent(fnUrlEvent({}))).toBe(true);
  });
  it("is false for an S3 event", () => {
    expect(isFunctionUrlEvent(s3Event)).toBe(false);
  });
});

describe("handlePosterHttp", () => {
  const deps = { sink: new StubPosterSink(), runWorkflow: async () => ({ ok: true, svg: "<svg/>", pngBase64: "AAAA" }) };

  it("returns 200 for a valid request", async () => {
    const res = await handlePosterHttp(fnUrlEvent({ performer: "K", venue: "F", date: "2026-08-15" }), deps);
    expect(res.statusCode).toBe(200);
    expect(JSON.parse(res.body).svg).toBe("<svg/>");
  });

  it("returns 400 for an invalid body", async () => {
    const res = await handlePosterHttp(fnUrlEvent({ performer: "K" }), deps);
    expect(res.statusCode).toBe(400);
    expect(JSON.parse(res.body).error).toBeTruthy();
  });

  it("returns 500 if the workflow throws", async () => {
    const res = await handlePosterHttp(
      fnUrlEvent({ performer: "K", venue: "F", date: "2026-08-15" }),
      { sink: new StubPosterSink(), runWorkflow: async () => { throw new Error("boom"); } },
    );
    expect(res.statusCode).toBe(500);
  });
});
