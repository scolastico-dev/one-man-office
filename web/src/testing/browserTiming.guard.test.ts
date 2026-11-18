import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'
import { expect, test } from 'vitest'

// Browser actions must wait on product state; scripted agents must be released
// by the test, rather than giving the browser a head start of N milliseconds.
test('browser tests never use wall-clock sleeps as completion barriers', () => {
  const root = resolve('e2e')
  const failures: string[] = []
  for (const name of readdirSync(root, { recursive: true, encoding: 'utf8' })) {
    if (!name.endsWith('.ts')) continue
    const text = readFileSync(resolve(root, name), 'utf8')
    for (const [i, line] of text.split('\n').entries()) {
      if (/\.waitForTimeout\s*\(|['"`]sleep\|/.test(line)) failures.push(`${name}:${i + 1}: ${line.trim()}`)
    }
  }
  expect(failures).toEqual([])
})

test('settings layout checks wait for loaded content rather than an absent loader', () => {
  const root = resolve('e2e')
  const failures: string[] = []
  for (const name of readdirSync(root)) {
    if (!name.endsWith('.spec.ts')) continue
    const lines = readFileSync(resolve(root, name), 'utf8').split('\n')
    for (const [i, line] of lines.entries()) {
      if (line.includes('[data-settings-loading]') && line.includes('toHaveCount(0)')) failures.push(`${name}:${i + 1}`)
    }
  }
  expect(failures).toEqual([])
})
