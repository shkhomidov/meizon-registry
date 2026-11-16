import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The dev server proxies /api to registryd so the browser sees a single origin
// and the HttpOnly session cookie flows without CORS. Override the target with
// VITE_API_TARGET.
// BASE PATH: the console is served under /registry/ on the shared host
// app.meizon.io, beside an unrelated Next.js admin app and the control-plane
// API. Without this every asset URL in index.html is absolute from the root --
// /assets/index-abc.js -- which on the shared host is a different service
// entirely and returns that service's 404, not this one's.
//
// Overridable so a standalone deployment (the old framework.meizon.io shape,
// or a self-hosted mirror at the root) builds with base '/' and keeps working
// unchanged.
const base = process.env.CONSOLE_BASE || '/registry/'

export default defineConfig({
  base,
  plugins: [react()],
  server: {
    port: Number(process.env.PORT) || 5173,
    proxy: {
      '/api': {
        target: process.env.VITE_API_TARGET || 'http://localhost:8088',
        changeOrigin: true,
      },
    },
  },
})
