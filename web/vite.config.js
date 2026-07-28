import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// Dev: proxy API + upload to the master on :8080.
// Prod: same-origin (Caddy reverse-proxies to master, or master serves the SPA).
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/upload': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
