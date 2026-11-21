import { readdirSync, readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// A path of the machine under test is compared with `isBelow` (e2e/support/within.ts), never as text with a
// hard-coded '/' or with no separator at all: Windows writes backslashes, and `/tmp2` begins with `/tmp`.
const HAND_WRITTEN = /\.startsWith\(\s*[\w.]+\s*(?:\+\s*(?:'\/'|"\/"|`\/`|sep|path\.sep)\s*)?\)/

const walk = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    if (e.name.startsWith('.') || e.name === 'node_modules') return []
    const path = join(dir, e.name)
    return e.isDirectory() ? walk(path) : /\.(ts|mts|mjs)$/.test(e.name) ? [path] : []
  })

// Only a variable argument is a folder prefix: a literal ('/', 'http') is a different kind of text test.
const containment = (text: string) => text.split('\n').filter((line) => HAND_WRITTEN.test(line) && /(?:root|tmp|home|work|base|dir|folder|parent)\b/i.test(line))

describe('path containment in the browser test support code', () => {
  it('recognises a hand-written prefix test', () => {
    expect(containment("if (!root.startsWith(tmp + '/')) throw 1")).toHaveLength(1)
    expect(containment('if (!home.startsWith(root)) throw 1')).toHaveLength(1)
    expect(containment('if (!p.startsWith(work + path.sep)) throw 1')).toHaveLength(1)
    expect(containment("if (!isBelow(tmp, root)) throw 1")).toHaveLength(0)
    expect(containment("path.startsWith('/') ? path : `/${path}`")).toHaveLength(0)
  })
  it.each(walk(resolve('e2e')))('is not used in e2e: %s', (path) => {
    const found: string[] = []
    if (path.endsWith('within.ts')) return
    for (const line of containment(readFileSync(path, 'utf8'))) found.push(`${path.slice(resolve('.').length + 1)}: ${line.trim()}`)
    expect(found).toEqual([])
  })
})
