import { defineConfig } from 'vite'

// Build to ./dist with relative asset paths so the Go binary can embed and
// serve it from any base. In dev, proxy API calls to the Go backend on 8651.
export default defineConfig({
  base: './',
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8651',
        changeOrigin: true,
      },
    },
  },
})
