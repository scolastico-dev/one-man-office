import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { fixedBrowserWaits } from './timingGuard'
import { guardSources } from './testing/guardSources'

const root = join(__dirname, '..')
const files = guardSources(root)

describe('browser synchronization guard', () => {
  it('rejects fixed browser waits including optional calls and spacing', () => {
    expect(fixedBrowserWaits('await page.waitForTimeout(100)')).toHaveLength(1)
    expect(fixedBrowserWaits('await page?.waitForTimeout (100)')).toHaveLength(1)
    expect(fixedBrowserWaits('await page.waitForTimeout?.(100)')).toHaveLength(1)
  })

  it('ignores documentation, strings, and condition-based waits', () => {
    expect(fixedBrowserWaits(`// page.waitForTimeout(100)
      const example = 'page.waitForTimeout(100)'
      const expression = /page.waitForTimeout(100)/
      /* await page.waitForTimeout(100) */
      await page.waitForFunction(() => window.ready)
      await expect(page.getByText('Ready')).toBeVisible()
    `)).toEqual([])
  })

  it('enumerates the complete source set', () => {
    expect(files.length).toBeGreaterThan(20)
  })

  it.each(files)('keeps browser tests and support helpers free of fixed waits: %s', (path) => {
    expect(fixedBrowserWaits(readFileSync(path, 'utf8')), path).toEqual([])
  })
})
