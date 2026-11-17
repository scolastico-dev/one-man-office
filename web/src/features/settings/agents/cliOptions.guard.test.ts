// The agent programs the profile form offers are exactly the CLI values the
// server accepts (the CLI constants in internal/api/settings.go).
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from 'vitest'
import { CLI_OPTIONS } from './profileForm'

const ROOT = join(__dirname, '../../../../..')

test('the profile form offers every CLI the server knows and no other', () => {
  const go = readFileSync(join(ROOT, 'internal/api/settings.go'), 'utf8')
  const block = /\/\/ Values of Profile\.CLI\.\nconst \(([\s\S]*?)\n\)/.exec(go)?.[1] ?? ''
  const server = [...block.matchAll(/\bCLI[A-Za-z]+\s*=\s*"([a-z]+)"/g)].map((m) => m[1]!)
  expect(server).toContain('agy')
  expect(CLI_OPTIONS.map((o) => o.value).sort()).toEqual([...server].sort())
})
