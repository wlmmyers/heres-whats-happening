import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In dev, the SPA is served by Vite on :5173. All requests to /api/* are
// proxied to the Go API on :8080 (which is /healthz, /auth/*, /me/*, etc.).
// In production, VITE_API_BASE_URL points to api.example.com.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    host: '127.0.0.1',
    strictPort: true,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  },
});
