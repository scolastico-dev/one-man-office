import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'
import { sourceInventory } from './sourceInventory'

test('source inventory retains every nested guard input and only the existing generated-directory exclusions', () => {
  const root = mkdtempSync(join(tmpdir(), 'omo-source-inventory-'))
  try {
    for (const name of ['src/a.ts', 'src/nested/b.js', 'e2e/support/c.mjs', 'scripts/tool.ts', 'vite.config.ts',
      'src/type.d.ts', 'src/view.vue', '.hidden/a.ts', 'node_modules/a.ts', 'dist/a.ts', 'test-results/a.ts', 'playwright-report/a.ts']) {
      const file = join(root, name)
      mkdirSync(join(file, '..'), { recursive: true })
      writeFileSync(file, 'export const value = 1')
    }
    expect(sourceInventory(root).map((file) => relative(root, file).split('\\').join('/'))).toEqual([
      'e2e/support/c.mjs', 'scripts/tool.ts', 'src/a.ts', 'src/nested/b.js', 'src/type.d.ts', 'vite.config.ts',
    ])
    expect(sourceInventory(join(root, 'src'), join(root, 'e2e')).map((file) => relative(root, file).split('\\').join('/'))).toEqual([
      'e2e/support/c.mjs', 'src/a.ts', 'src/nested/b.js', 'src/type.d.ts',
    ])
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
