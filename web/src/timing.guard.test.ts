import { readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'
import { fixedBrowserWaits } from './timingGuard'
import { sourceInventory } from './testing/sourceInventory'

const root = join(__dirname, '..')
const files = sourceInventory(root)

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

  it('retains the complete web source, browser test and tooling inventory', () => {
    expect(files.length).toBeGreaterThan(20)
  })

  it.each(files.map((file) => [relative(root, file), file] as const))('keeps %s free of fixed waits', (_name, file) => {
    expect(fixedBrowserWaits(readFileSync(file, 'utf8')), file).toEqual([])
  })
})
