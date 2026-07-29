import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { vanillaExtractPlugin } from '@vanilla-extract/vite-plugin';

export default defineConfig({
  plugins: [vanillaExtractPlugin(), react()],
  test: {
    environment: 'happy-dom',
    globals: true,
    setupFiles: ['./src/setupTests.ts'],
    css: false,
    // Pin the zone so date-boundary tests mean the same thing on every machine
    // and in CI. Several assertions here depend on local-vs-UTC date rollover.
    env: { TZ: 'America/Los_Angeles' },
  },
});
