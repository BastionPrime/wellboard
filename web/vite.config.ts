/// <reference types="vitest" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Production: the built dist/ is embedded into the Go binary (web/embed.go,
// DECISIONS D13), so the SPA and the API share one origin — no CORS.
// Development: `vite` serves the SPA on :5173 and proxies /api to the local
// wellboard daemon started with `make run` (port 8090).
export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8090',
        changeOrigin: false,
      },
    },
  },
  test: {
    environment: 'node',
    include: ['src/**/*.spec.ts'],
  },
})
