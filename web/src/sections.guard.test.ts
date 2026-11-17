// A settings.changed event names its section as omo does (internal/api/events.go);
// a check against another word never matches and the list it guards is never
// read again. Every `section === '…'` in the app must be one of omo's sections.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'

const SRC = __dirname

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) return n === 'sample' ? [] : files(p)
    return /\.(ts|vue)$/.test(n) && !/\.test\.ts$/.test(n) ? [p] : []
  })
}

test('every settings section the app compares with is one omo sends', () => {
  const go = readFileSync(join(SRC, '../../internal/api/events.go'), 'utf8')
  const sections = new Set([...go.matchAll(/\bSection\w+\s*=\s*"([a-z]+)"/g)].map((m) => m[1]!))
  expect(sections.has('profiles')).toBe(true)
  const bad: string[] = []
  for (const f of files(SRC)) {
    const text = readFileSync(f, 'utf8')
    for (const m of text.matchAll(/\bsection\)?\s*[!=]==\s*'([a-z]+)'/g)) {
      if (!sections.has(m[1]!)) bad.push(`${relative(SRC, f).split('\\').join('/')}: ${m[1]}`)
    }
  }
  expect(bad).toEqual([])
})
