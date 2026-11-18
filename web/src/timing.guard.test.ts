import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { fixedBrowserWaits } from './timingGuard'

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

  it('keeps browser tests and support helpers free of fixed waits', () => {
    const root = join(__dirname, '..')
    let checked = 0
    const visit = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        if (entry.name.startsWith('.') || ['node_modules', 'dist', 'test-results', 'playwright-report'].includes(entry.name)) continue
        const path = join(dir, entry.name)
        if (entry.isDirectory()) visit(path)
        else if (/\.(ts|js|mjs)$/.test(entry.name)) {
          checked++
          expect(fixedBrowserWaits(readFileSync(path, 'utf8')), path).toEqual([])
        }
      }
    }
    visit(root)
    expect(checked).toBeGreaterThan(20)
  })
})
