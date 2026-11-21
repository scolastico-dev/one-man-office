// @vitest-environment node
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'
import { microtaskGuesses } from '../completionGuard'
import { fixedBrowserWaits } from '../timingGuard'
import { guardSources } from './guardSources'

test('source guards enumerate every supported source, including tests and nested helpers', () => {
  const root = mkdtempSync(join(tmpdir(), 'omo-guard-sources-'))
  try {
    const included = ['root.js', 'driver.mjs', 'nested/support.ts', 'nested/check.test.ts']
    const excluded = ['view.vue', 'style.css', 'module.mts', '.hidden.ts', '.cache/hidden.ts', 'node_modules/package.ts', 'dist/bundle.js', 'test-results/result.js', 'playwright-report/report.js', 'nested/dist/bundle.js']
    for (const path of [...included, ...excluded]) {
      const full = join(root, path)
      mkdirSync(join(full, '..'), { recursive: true })
      writeFileSync(full, '')
    }
    expect(guardSources(root).map((path) => relative(root, path).split('\\').join('/')).sort()).toEqual([...included].sort())

    // The collected cases must still reach a bad source, rather than passing on an empty enumeration.
    const bad = join(root, 'nested', 'support.ts')
    writeFileSync(bad, 'async function t() { for (let i = 0; i < 3; i++) await Promise.resolve(); await page.waitForTimeout(10) }')
    const completion = guardSources(root).filter((path) => microtaskGuesses(readFileSync(path, 'utf8')).length > 0)
    const timing = guardSources(root).filter((path) => fixedBrowserWaits(readFileSync(path, 'utf8')).length > 0)
    expect(completion).toEqual([bad])
    expect(timing).toEqual([bad])
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
