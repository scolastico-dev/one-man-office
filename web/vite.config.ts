/// <reference types="vitest/config" />
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

const target = process.env.OMO_DEV_TARGET ?? 'http://127.0.0.1:8090'

// Loads src/theme-boot.js as a classic blocking script at the top of <head>,
// so the remembered theme is set before the first paint. Inline scripts are
// not allowed by the server's Content-Security-Policy, and Vite only bundles
// module scripts, which run after the first paint; the build therefore emits
// the file as a content-hashed asset.
function themeBoot(): Plugin {
  const source = fileURLToPath(new URL('./src/theme-boot.js', import.meta.url))
  let build = false
  let fileName = ''
  return {
    name: 'omo-theme-boot',
    configResolved(config) {
      build = config.command === 'build'
    },
    buildStart() {
      if (!build) return
      const code = readFileSync(source)
      fileName = `assets/theme-boot-${createHash('sha256').update(code).digest('hex').slice(0, 8)}.js`
      this.emitFile({ type: 'asset', fileName, source: code })
    },
    transformIndexHtml() {
      return [{ tag: 'script', attrs: { src: build ? `/${fileName}` : '/src/theme-boot.js' }, injectTo: 'head' }]
    },
  }
}

export default defineConfig({
  plugins: [themeBoot(), vue(), tailwindcss()],
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    modulePreload: { polyfill: false },
    // Fonts must stay files (the Content-Security-Policy allows no data: fonts),
    // and the font licence must ship as a file next to them.
    assetsInlineLimit: (file) => (/\.(woff2|txt)$/.test(file) ? false : undefined),
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
    setupFiles: ['src/testing/setup.ts'],
    // Tests read stylesheets as text (`?raw`) to check the tokens.
    css: { include: [/\.css\?raw$/] },
  },
})
