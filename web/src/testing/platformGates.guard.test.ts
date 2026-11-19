import { readdirSync, readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// A helper that answers "nothing found" on a platform it cannot inspect turns a proof into a pass: the test goes green
// without having observed anything. A platform may differ in HOW a helper observes (see support/process.ts), never in
// WHETHER it does. This finds `if (process.platform … ) return` and the same with a value, in the browser test code.
const SUCCESS_BY_ABSENCE = /if\s*\(\s*process\.platform\s*[!=]==?\s*['"][a-z0-9]+['"]\s*\)\s*return\b/

const walk = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    if (e.name.startsWith('.') || e.name === 'node_modules') return []
    const path = join(dir, e.name)
    return e.isDirectory() ? walk(path) : /\.(ts|mts|mjs)$/.test(e.name) ? [path] : []
  })

const gates = (text: string) => text.split('\n').filter((line) => SUCCESS_BY_ABSENCE.test(line))

describe('helpers that answer success on a platform they cannot inspect', () => {
  it('recognises the gate', () => {
    expect(gates("  if (process.platform !== 'linux') return []")).toHaveLength(1)
    expect(gates("if (process.platform === 'win32') return")).toHaveLength(1)
    expect(gates("if (process.platform === 'win32') { cmd = 'x' }")).toHaveLength(0)
    expect(gates("const shell = process.platform === 'win32' ? 'cmd' : 'sh'")).toHaveLength(0)
  })
  it('is not in the browser test code', () => {
    const found: string[] = []
    for (const path of walk(resolve('e2e'))) for (const line of gates(readFileSync(path, 'utf8'))) found.push(`${path.slice(resolve('.').length + 1)}: ${line.trim()}`)
    expect(found).toEqual([])
  })
})
