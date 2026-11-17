/// <reference types="vitest/config" />
import { createHash } from 'node:crypto'
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
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

// Writes the licence texts of the npm packages whose code is in the built
// bundle to third-party-notices.txt (outside dist, not embedded); `make
// notices` puts them into THIRD_PARTY_NOTICES with the Go modules'. A
// bundled package without a licence file fails the build.
// VIRTUAL_HELPERS maps the generated modules of the build tools to the
// package whose code they are.
const VIRTUAL_HELPERS: [string, string][] = [
  ['rolldown/', 'rolldown'],
  ['vite/', 'vite'],
  ['plugin-vue:', '@vitejs/plugin-vue'],
]

// ownLicence cuts the licences of a package's own bundled dependencies
// (Vite lists them after its own) from its licence file: only the package's
// own helper code is in the app.
function ownLicence(text: string): string {
  const at = text.indexOf('\n# Licenses of bundled dependencies')
  return at < 0 ? text : text.slice(0, at)
}

function thirdPartyNotices(): Plugin {
  const out = fileURLToPath(new URL('./third-party-notices.txt', import.meta.url))
  return {
    name: 'omo-third-party-notices',
    apply: 'build',
    generateBundle(_options, bundle) {
      const modules = fileURLToPath(new URL('./node_modules/', import.meta.url))
      const dirs = new Set<string>()
      for (const chunk of Object.values(bundle)) {
        if (chunk.type !== 'chunk') continue
        for (const id of chunk.moduleIds) {
          if (id.startsWith('\0')) {
            // Helpers the build tools add: their own code, under their
            // packages' licences.
            const tool = VIRTUAL_HELPERS.find(([prefix]) => id.startsWith('\0' + prefix))
            if (!tool) this.error(`no package known for the generated module ${id.slice(1)}`)
            dirs.add(join(modules, tool[1]))
            continue
          }
          const m = /^(.*\/node_modules\/(?:@[^/]+\/)?[^/]+)\//.exec(id.replace(/\\/g, '/'))
          if (m?.[1]) dirs.add(m[1])
        }
      }
      const parts: string[] = []
      for (const dir of [...dirs].sort()) {
        const pkg = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8')) as { name: string; version: string; license?: string }
        const files = readdirSync(dir).filter((f) => /^(licen[cs]e|copying|notice)/i.test(f)).sort()
        if (files.length === 0) this.error(`${pkg.name} is bundled but ships no licence file`)
        parts.push(`${'='.repeat(72)}\n${pkg.name} ${pkg.version} (npm)\nLicense: ${pkg.license ?? 'see below'}\n${'-'.repeat(72)}\n` +
          files.map((f) => ownLicence(readFileSync(join(dir, f), 'utf8')).trimEnd()).join('\n\n') + '\n')
      }
      if (!existsSync(dirname(out))) this.error(`no folder for ${out}`)
      writeFileSync(out, parts.join('\n'))
    },
  }
}

export default defineConfig({
  plugins: [themeBoot(), vue(), tailwindcss(), thirdPartyNotices()],
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    modulePreload: { polyfill: false },
    // Keep the dependencies' licence comments (@license, /*! ... */) in the bundle.
    rolldownOptions: { output: { comments: { legal: true } } },
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
