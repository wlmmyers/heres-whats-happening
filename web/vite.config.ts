import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { vanillaExtractPlugin } from '@vanilla-extract/vite-plugin';

// Local API proxy. Shared by the dev server and `vite preview`, which is served
// by a separate server and so needs its own copy of the config.
const apiProxy = {
  '/api': {
    target: 'http://localhost:8080',
    changeOrigin: true,
    rewrite: (path: string) => path.replace(/^\/api/, ''),
  },
};

export default defineConfig({
  plugins: [vanillaExtractPlugin(), react()],
  server: {
    port: 5173,
    host: '127.0.0.1',
    strictPort: true,
    proxy: apiProxy,
  },
  // Serves the production build from dist/ (see `pnpm start:prod`).
  preview: {
    port: 4173,
    host: '127.0.0.1',
    strictPort: true,
    proxy: apiProxy,
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
});
