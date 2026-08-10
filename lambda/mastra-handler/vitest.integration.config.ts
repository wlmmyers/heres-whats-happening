import { defineConfig } from "vitest/config";

// Integration specs only. Requires the docker-compose stack (ElasticMQ on :9324):
//   docker compose up -d elasticmq
// Deliberately kept out of the default run and out of CI, neither of which has
// the stack. Failures here are real failures, not "the service wasn't up" —
// that is why these are opt-in rather than silently self-skipping.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.integration.test.ts"],
    setupFiles: ["src/vitest.setup.ts"],
  },
});
