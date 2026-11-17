import { fileURLToPath } from 'node:url'
import { defineConfig } from '@playwright/test'
import base from '../../playwright.config'

/** A path next to this file. */
const here = (p: string) => fileURLToPath(new URL(p, import.meta.url))

// The README's screenshots (see readme.shots.ts): the browser-test harness
// with its own file pattern, so the e2e suite never runs them. Run with
// `make readme-shots` or `npm --prefix web run readme-shots`.
export default defineConfig({
  ...base,
  testDir: here('.'),
  testMatch: '*.shots.ts',
  workers: 1,
  fullyParallel: false,
  timeout: 240_000,
  reporter: [['list']],
  use: {
    ...base.use,
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    trace: 'off',
  },
})
