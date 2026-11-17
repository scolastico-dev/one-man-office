// A setting has one wording everywhere it is shown (Create dialog, ticket
// details, settings pages): its labels live in features/ticket/fieldRules.ts
// and nothing else defines a table of them.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'

const SRC = __dirname

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) return files(p)
    return /\.(ts|vue)$/.test(n) && !/\.test\.ts$/.test(n) ? [p] : []
  })
}

test('the labels of review, when approved and task release are defined once', () => {
  const bad: string[] = []
  for (const f of files(SRC)) {
    if (relative(SRC, f) === join('features', 'ticket', 'fieldRules.ts')) continue
    readFileSync(f, 'utf8')
      .split('\n')
      .forEach((line, i) => {
        if (/\b(REVIEW|WHEN|APPROVED|RELEASE)_LABEL\b\s*(:[^=]*)?=/.test(line)) bad.push(`${relative(SRC, f)}:${i + 1}: ${line.trim()}`)
      })
  }
  expect(bad).toEqual([])
})
