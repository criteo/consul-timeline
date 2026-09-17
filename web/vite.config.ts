import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The UI is served by the Go binary under /web/ from public/dist, which
// go:embed packs into the binary. In development, `vite` proxies the API
// to a locally running consul-timeline.
export default defineConfig({
  plugins: [react()],
  base: '/web/',
  build: {
    outDir: '../public/dist',
    emptyOutDir: false, // the Makefile cleans it and keeps .gitkeep for go:embed
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8888', changeOrigin: true },
    },
  },
})
