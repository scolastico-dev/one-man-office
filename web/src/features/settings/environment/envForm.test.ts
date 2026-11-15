import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test } from 'vitest'
import { checkedOf, currentChecks, formErrors, isShipped, rowErrors, shippedCopy, SHIPPED_ENV, summaryOf, toneOf, variableOf } from './envForm'

test("shipped defaults match the server's", () => {
  const go = readFileSync(resolve(import.meta.dirname, '../../../../../internal/settings/general.go'), 'utf8')
  const block = /AgentEnv: map\[string\]string\{([\s\S]*?)\n\t\t\},/.exec(go)
  expect(block, 'the AgentEnv defaults are in general.go').not.toBeNull()
  const entries = [...block![1]!.matchAll(/^\s*"([A-Z_]+)":\s+"(.*)",$/gm)].map((m) => [m[1]!, m[2]!])
  expect(Object.fromEntries(entries)).toEqual(SHIPPED_ENV)
  expect(entries).toHaveLength(5)
})

test('shipped copy is a fresh copy and is recognised as the defaults', () => {
  const copy = shippedCopy()
  expect(isShipped(copy)).toBe(true)
  copy.PAGER = 'cat'
  expect(isShipped(copy)).toBe(false)
  expect(SHIPPED_ENV.PAGER).toBeUndefined()
  expect(isShipped({})).toBe(false)
})

test('a refusal is placed under the variable it names', () => {
  expect(variableOf('agent_env.FOO')).toBe('FOO')
  expect(variableOf('agent_env')).toBeNull()
  expect(variableOf('health.silent_after')).toBeNull()
  const errors = { 'agent_env.FOO': 'Enter the value of FOO again.', agent_env: 'too many' }
  expect(rowErrors(errors)).toEqual({ FOO: 'Enter the value of FOO again.' })
  expect(formErrors(errors)).toEqual(['too many'])
})

test('an answer is dropped as soon as its row is edited or removed', () => {
  const sent = { A: '`echo ok`', B: '`false`', C: 'x' }
  const results = checkedOf(
    {
      variables: [
        { name: 'A', state: 'set', message: '' },
        { name: 'B', state: 'failed', message: 'its command failed' },
        { name: 'GONE', state: 'set', message: '' },
      ],
    },
    sent,
  )
  expect(results.map((r) => r.check.name)).toEqual(['A', 'B'])
  expect(currentChecks(results, sent).map((c) => c.name)).toEqual(['A', 'B'])
  expect(currentChecks(results, { ...sent, B: '`true`' }).map((c) => c.name)).toEqual(['A'])
  const { A: _a, ...without } = sent
  expect(currentChecks(results, without).map((c) => c.name)).toEqual(['B'])
})

test('the summary counts in words', () => {
  const set = { name: 'A', state: 'set' as const, message: '' }
  const failed = { name: 'B', state: 'failed' as const, message: 'm' }
  const empty = { name: 'C', state: 'empty' as const, message: '' }
  expect(summaryOf([set])).toBe('1 variable checked, all set')
  expect(summaryOf([set, failed, empty])).toBe('1 of 3 variables failed')
  expect(summaryOf([set, empty])).toBe('1 of 2 variables empty')
  expect([toneOf([set]), toneOf([set, empty]), toneOf([set, empty, failed])]).toEqual(['info', 'warn', 'danger'])
})
