import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from 'vitest'

// An agent script that ends in `exit` without handing in is a failure to the engine, which starts a fresh agent of that role
// until the third failure in a row. The sessions of one test are then not one, and a row addressed by its position can be a later
// attempt. A spec that uses such a script for a role the engine restarts (the CEO is never restarted by itself) waits for the final
// state with `waitForRestartsExhausted` and acts on a session it picked by id, or it already holds a session by id because the
// script waits for the test (`await-line`) before it exits.
const e2e = join(__dirname, '../../e2e')
const specs = readdirSync(e2e).filter((f) => f.endsWith('.spec.ts'))

/** The text of a call from its opening parenthesis to the one that closes it. */
function callBody(text: string, open: number): string {
  let depth = 0
  for (let i = open; i < text.length; i++) {
    if (text[i] === '(') depth++
    else if (text[i] === ')' && --depth === 0) return text.slice(open + 1, i)
  }
  return text.slice(open + 1)
}

/** The roles of the scripts in a spec that end in `exit|`, with the line each script starts at. */
function exitingRoles(text: string): { role: string; line: number }[] {
  const found: { role: string; line: number }[] = []
  for (const m of text.matchAll(/\b(developer|reviewer|pm|ceo)\s*:\s*script\(/g)) {
    if (/['"`]exit\|/.test(callBody(text, m.index + m[0].length - 1))) found.push({ role: m[1]!, line: text.slice(0, m.index).split('\n').length })
  }
  return found
}

test('the guard sees the specs and the exiting scripts', () => {
  expect(specs.length).toBeGreaterThan(50)
  expect(exitingRoles(readFileSync(join(e2e, 'wheel-states.spec.ts'), 'utf8')).map((r) => r.role)).toEqual(['developer'])
  expect(exitingRoles(readFileSync(join(e2e, 'ceo.spec.ts'), 'utf8')).map((r) => r.role)).toContain('ceo')
})

test('a spec whose restarted role exits at once waits for the last attempt and picks a session by id', () => {
  const offenders: string[] = []
  for (const f of specs) {
    const text = readFileSync(join(e2e, f), 'utf8')
    for (const { role, line } of exitingRoles(text)) {
      if (role === 'ceo') continue
      const waits = /['"`]await-line\|/.test(text.split('\n').slice(line - 1, line + 2).join('\n'))
      if (!waits && !text.includes("waitForRestartsExhausted(omo")) offenders.push(`${f}:${line} (${role})`)
      // A row of such a test is never the first one by position.
      if (!waits && /\[data-session\]['"`)]*\s*\)?\s*\.first\(\)|li\[data-session\]['"`)]*\)\s*\.first\(\)/.test(text)) offenders.push(`${f}: a session row picked with first()`)
    }
  }
  expect(offenders).toEqual([])
})
