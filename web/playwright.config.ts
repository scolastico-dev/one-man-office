import { fileURLToPath } from 'node:url'
import { defineConfig, devices } from '@playwright/test'

/** A path next to this file. Playwright resolves relative paths against the config it loaded, which is the one at the repository root when run from there. */
const here = (p: string) => fileURLToPath(new URL(p, import.meta.url))

// Browser tests run against the real omo binary, started per test (see
// e2e/support/omo.ts). Traces and screenshots are kept only for failures, in
// web/test-results/ (ignored by git), wherever the run starts: from web/
// (npm run e2e) or from the repository root (npx --prefix web playwright
// test, which finds ../playwright.config.ts, a re-export of this file).
export default defineConfig({
  testDir: here('./e2e'),
  testMatch: '**/*.spec.ts',
  globalSetup: here('./e2e/global-setup.ts'),
  outputDir: here('./test-results'),
  fullyParallel: true,
  workers: process.env.CI ? 2 : undefined,
  retries: 0,
  forbidOnly: !!process.env.CI,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [['list'], ['html', { open: 'never', outputFolder: here('./playwright-report') }]] : [['list']],
  use: {
    ...devices['Desktop Chrome'],
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
    actionTimeout: 10_000,
    navigationTimeout: 15_000,
    locale: 'en-US',
    // Animations finish at once, so tests never wait for a transition.
    reducedMotion: 'reduce',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
})
