import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The dev server proxies /api to registryd so the browser sees a single origin
// and the HttpOnly session cookie flows without CORS. Override the target with
// VITE_API_TARGET.
export default defineConfig({
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
