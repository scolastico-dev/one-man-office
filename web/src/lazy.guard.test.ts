// Every part of the app fetched on demand must show something when the fetch
// fails (omo updated while the tab stayed open): a dynamic import is allowed
// only through lazyComponent / lazyRoute (components/ui/chunkFailure.ts).
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'

const SRC = __dirname

/** Files whose dynamic imports are wrapped elsewhere, with the place that wraps them. */
const WRAPPED_ELSEWHERE: Record<string, string> = {
  // The section loaders: SettingsView builds each page with lazyComponent.
  'features/settings/settingsSections.ts': 'features/settings/SettingsView.vue',
}

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) return files(p)
    return /\.(ts|vue)$/.test(n) && !/\.test\.ts$/.test(n) ? [p] : []
  })
}

test('every dynamic import shows a message with Reload when its code cannot be fetched', () => {
  const bad: string[] = []
  for (const f of files(SRC)) {
    const rel = relative(SRC, f).split('\\').join('/')
    const lines = readFileSync(f, 'utf8').split('\n')
    lines.forEach((line, i) => {
      if (!/\bimport\(\s*['"]/.test(line)) return
      if (/\b(lazyComponent|lazyRoute)\(/.test(line)) return
      const wrapper = WRAPPED_ELSEWHERE[rel]
      if (wrapper !== undefined && /\blazyComponent\(/.test(readFileSync(join(SRC, wrapper), 'utf8'))) return
      bad.push(`${rel}:${i + 1}: ${line.trim()}`)
    })
  }
  expect(bad).toEqual([])
})
