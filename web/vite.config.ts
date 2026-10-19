import { svelte } from '@sveltejs/vite-plugin-svelte'
import { defineConfig } from 'vitest/config'

// The dev server proxies the API (and its WebSocket) to a running Fetcharr
// server; the compose file publishes it on 8083.
const target = process.env.FETCHARR_URL ?? 'http://localhost:8083'

export default defineConfig({
  // Relative asset URLs plus hash routing keep the UI working behind a
  // reverse-proxy path prefix.
  base: './',
  plugins: [svelte()],
  build: {
    // Embedded into the server binary by server/internal/webui.
    outDir: '../server/internal/webui/dist/app',
    emptyOutDir: true,
    target: 'es2022',
  },
  server: {
    proxy: { '/api': { target, ws: true } },
  },
  test: {
    include: ['src/**/*.test.ts'],
  },
})
