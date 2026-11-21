import { readdirSync } from 'node:fs'
import { join } from 'node:path'

/** The complete source inventory used by the syntax guards. */
export function sourceInventory(...roots: string[]): string[] {
  const visit = (dir: string): string[] => readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    if (entry.name.startsWith('.') || ['node_modules', 'dist', 'test-results', 'playwright-report'].includes(entry.name)) return []
    const file = join(dir, entry.name)
    return entry.isDirectory() ? visit(file) : /\.(ts|js|mjs)$/.test(entry.name) ? [file] : []
  })
  return roots.flatMap(visit).sort()
}
