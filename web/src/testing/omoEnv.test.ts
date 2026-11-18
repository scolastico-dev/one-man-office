import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { devNull, tmpdir } from 'node:os'
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


it('removes inherited shell startup, editor, option, prompt, history and function overrides', () => {
  const names = [
    'ENV', 'BASH_ENV', 'ZDOTDIR', 'INPUTRC', 'SHELLOPTS', 'BASHOPTS', 'BASH_COMPAT', 'POSIXLY_CORRECT',
    'BASH_FUNC_echo%%', 'BASH_XTRACEFD', 'BASH_LOADABLES_PATH',
    'EDITOR', 'VISUAL', 'CDPATH', 'GLOBIGNORE', 'FIGNORE', 'KEYTIMEOUT', 'ZLE_RPROMPT_INDENT',
    'PROMPT_COMMAND', 'PROMPT_DIRTRIM', 'PS0', 'PS1', 'PS2', 'PS3', 'PS4', 'PSVAR',
    'PROMPT', 'PROMPT2', 'RPROMPT', 'RPROMPT2', 'RPS1', 'RPS2', 'SPROMPT',
    'HISTFILE', 'HISTSIZE', 'HISTFILESIZE', 'HISTCONTROL', 'HISTIGNORE', 'HISTTIMEFORMAT', 'HISTCHARS', 'SAVEHIST',
  ]
  for (const name of names) {
    vi.stubEnv(name, 'hostile-runner-config')
    vi.stubEnv(name.toLowerCase(), 'hostile-runner-config')
  }
  vi.stubEnv('LANG', 'en_US.UTF-8')
  const env = cleanEnv('/temporary/omo-test')
  // Readline must also ignore a machine-wide /etc/inputrc fallback.
  expect(env.INPUTRC).toBe(devNull)
  for (const name of names.filter((name) => name !== 'INPUTRC')) expect(env[name], name).toBeUndefined()
  for (const name of names) expect(env[name.toLowerCase()], name.toLowerCase()).toBeUndefined()
  expect(env.LANG).toBe('en_US.UTF-8')
})

it.skipIf(process.platform === 'win32')('isolates real Bash startup code and Readline from hostile runner files', () => {
  const root = mkdtempSync(join(tmpdir(), 'omo-shell-env-'))
  try {
    const home = join(root, 'home')
    mkdirSync(home)
    const startup = join(root, 'startup')
    const inputrc = join(root, 'inputrc')
    writeFileSync(startup, 'exit 93\n')
    writeFileSync(inputrc, 'set editing-mode vi\nset enable-bracketed-paste off\n')
    vi.stubEnv('BASH_ENV', startup)
    vi.stubEnv('ENV', startup)
    vi.stubEnv('INPUTRC', inputrc)
    vi.stubEnv('SHELLOPTS', 'posix')
    vi.stubEnv('BASHOPTS', 'nullglob')
    vi.stubEnv('POSIXLY_CORRECT', '1')
    // Select Bash even when the developer uses Zsh.
    vi.stubEnv('SHELL', terminalShell(undefined, process.env.PATH))
    const env: NodeJS.ProcessEnv = { ...cleanEnv(home), TERM: 'xterm' }
    const bash = env.SHELL!
    const run = (args: string[], childEnv = env) => spawnSync(bash, args, { env: childEnv, encoding: 'utf8', timeout: 5_000 })
    // Controls prove that these files affect this actual shell when passed on.
    const control: NodeJS.ProcessEnv = { ...env }
    for (const key of ['BASH_ENV', 'ENV', 'INPUTRC', 'SHELLOPTS', 'BASHOPTS', 'POSIXLY_CORRECT']) delete control[key]
    expect(run(['-c', 'exit 0'], { ...control, BASH_ENV: startup }).status).toBe(93)
    expect(run(['-ic', 'exit 0'], { ...control, ENV: startup, POSIXLY_CORRECT: '1' }).status).toBe(93)
    // Editing mode is also available in macOS's older system Readline.
    expect(run(['-ic', 'bind -v'], { ...control, INPUTRC: inputrc }).stdout).toContain('set editing-mode vi')
    const plain = run(['-c', 'printf clean; shopt -q nullglob && exit 94; exit 0'])
    expect(plain.error).toBeUndefined()
    expect(plain.status).toBe(0)
    expect(plain.stdout).toBe('clean')
    const interactive = run(['-ic', 'bind -v'])
    expect(interactive.error).toBeUndefined()
    expect(interactive.status).toBe(0)
    expect(interactive.stdout).toContain('set editing-mode emacs')
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})


it('all browser child environments inherit through the central shell-config filter', () => {
  const root = resolve('e2e')
  const failures: string[] = []
  for (const name of readdirSync(root, { recursive: true, encoding: 'utf8' })) {
    if (!name.endsWith('.ts') || resolve(root, name) === resolve(root, 'support/env.ts')) continue
    const text = readFileSync(resolve(root, name), 'utf8')
    // Individual reads (PATH, CPU settings, etc.) are fine. Inheriting the
    // entire environment must go through withoutShellConfig or cleanEnv.
    if (/\bprocess\s*\.\s*env\b(?!\s*(?:\.|\[))/.test(text)) failures.push(name)
  }
  expect(failures).toEqual([])
})
