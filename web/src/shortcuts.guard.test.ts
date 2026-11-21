// The single-key shortcuts share one rule (components/ui/shortcuts.ts): no
// second copy of the typing check, and no check of an open dialog of its own.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'

const SRC = __dirname
const HOME = 'components/ui/shortcuts.ts'

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) return files(p)
    return /\.(ts|vue)$/.test(n) && !/\.test\.ts$/.test(n) ? [p] : []
  })
}

test.each(files(SRC))('the shortcut rule lives in shortcuts.ts only: %s', (f) => {
  const bad: string[] = []
  const rel = relative(SRC, f).split('\\').join('/')
  if (rel === HOME) return
  const text = readFileSync(f, 'utf8')
  text.split('\n').forEach((line, i) => {
    if (/function typing\s*\(/.test(line) || /querySelector(All)?\(\s*['"]dialog\[open\]/.test(line)) bad.push(`${rel}:${i + 1}: ${line.trim()}`)
  })
  expect(bad).toEqual([])
})
