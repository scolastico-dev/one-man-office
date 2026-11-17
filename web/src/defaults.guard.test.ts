// A task in a story has defaults of its own (`story_task`): every reader of
// the review and "when approved" defaults picks the entry with defaultKind
// (features/ticket/fieldRules.ts), never by the ticket type alone.
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

test('the review and when-approved defaults are read through defaultKind', () => {
  const bad: string[] = []
  for (const f of files(SRC)) {
    readFileSync(f, 'utf8')
      .split('\n')
      .forEach((line, i) => {
        for (const m of line.matchAll(/(review|when_approved)_defaults\[([^\]]*)\]?/g)) {
          if (!m[2]!.startsWith('defaultKind(')) bad.push(`${relative(SRC, f)}:${i + 1}: ${line.trim()}`)
        }
      })
  }
  expect(bad).toEqual([])
})
