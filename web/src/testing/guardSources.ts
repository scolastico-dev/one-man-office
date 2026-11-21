import { readdirSync } from 'node:fs'
import { join } from 'node:path'

/** Enumerate during collection so each source can have its own completed rule check. */
export function guardSources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    if (entry.name.startsWith('.') || ['node_modules', 'dist', 'test-results', 'playwright-report'].includes(entry.name)) return []
    const path = join(dir, entry.name)
    return entry.isDirectory() ? guardSources(path) : /\.(ts|js|mjs)$/.test(entry.name) ? [path] : []
  })
}
