/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

const target = process.env.OMO_DEV_TARGET ?? 'http://127.0.0.1:8090'

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    modulePreload: { polyfill: false },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target,
        changeOrigin: true,
        configure(proxy) {
          proxy.on('proxyReq', (req) => {
            req.removeHeader('origin')
          })
        },
      },
    },
  },
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'],
  },
})
