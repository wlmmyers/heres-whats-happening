import { configDefaults, defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
    // Integration specs (*.integration.test.ts) need the docker-compose stack —
    // ElasticMQ on :9324 — so they are excluded from the default run. This is
    // what lets CI call a bare `pnpm test` instead of maintaining a whitelist of
    // every other spec file, a list that silently omits any new test nobody
    // remembers to add. Run them with `pnpm test:integration`.
    exclude: [...configDefaults.exclude, '**/*.integration.test.ts'],
    setupFiles: ['src/vitest.setup.ts'],
  },
});
