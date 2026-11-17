// A button disabled while it has the focus must keep it (components/ui/vDisabled.ts):
// every button takes `v-disabled`, never the native `disabled` attribute or a
// `:disabled` binding of its own.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'

const SRC = __dirname

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) return files(p)
    return n.endsWith('.vue') ? [p] : []
  })
}

test('no button binds the native disabled attribute', () => {
  const bad: string[] = []
  for (const f of files(SRC)) {
    const text = readFileSync(f, 'utf8')
    for (const m of text.matchAll(/<button\b[^>]*>/g)) {
      if (/\s:?disabled(=|\s|>)/.test(m[0]) || /\sv-bind:disabled=/.test(m[0])) {
        const line = text.slice(0, m.index).split('\n').length
        bad.push(`${relative(SRC, f).split('\\').join('/')}:${line}`)
      }
    }
  }
  expect(bad, 'use v-disabled from components/ui/vDisabled.ts').toEqual([])
})
