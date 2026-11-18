import { readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { afterEach, expect, it, vi } from 'vitest'
import { cleanEnv, terminalShell } from '../../e2e/support/env'

afterEach(() => vi.unstubAllEnvs())

it('keeps only test-owned omo variables in server, CLI and seed environments', () => {
  // Share the Go guards' complete set of known names, without reading any session value.
  const samples = readFileSync(resolve('..', 'internal/childenv/childenvtest/testdata/omo-variables.txt'), 'utf8')
  const names = new Set(samples.trim().split(/\s+/))
  for (const key of names) vi.stubEnv(key, '/must-never-contact/omo.sock')
  const home = '/temporary/omo-test'
  const env = cleanEnv(home, '/test/programs')
  expect(Object.fromEntries(Object.entries(env).filter(([key]) => key.toUpperCase().startsWith('OMO_')))).toEqual({
    OMO_HOME: join(home, 'omo'),
    OMO_PLUGIN_CATALOG: join(home, 'plugin-list.git'),
  })
})

it.skipIf(process.platform === 'win32')('uses a shell with line editing and bracketed paste, regardless of the runner login shell', () => {
  vi.stubEnv('SHELL', '/a/non-POSIX/login-shell')
  const env = cleanEnv('/temporary/omo-test', '/test/programs')
  expect(env.SHELL).toMatch(/(?:^|\/)bash$/)
})

it.skipIf(process.platform === 'win32')('preserves compatible interactive shells and resolves Bash for other login shells', () => {
  for (const shell of ['/opt/bin/bash', '/opt/bin/zsh']) {
    expect(terminalShell(shell, '/tools', (p) => p === shell)).toBe(shell)
  }
  expect(terminalShell('/bin/fish', '/tools:/elsewhere', (p) => p === '/tools/bash')).toBe('/tools/bash')
  expect(terminalShell('/bin/sh', '', (p) => p === '/bin/bash')).toBe('/bin/bash')
  expect(() => terminalShell('/bin/fish', '', () => false)).toThrow('Bash with line editing and bracketed paste')
})
