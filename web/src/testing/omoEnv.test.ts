import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { devNull, tmpdir } from 'node:os'
import { delimiter, join, resolve, sep } from 'node:path'
import { afterEach, expect, it, vi } from 'vitest'
import { cleanEnv, findGit, installGit, terminalShell } from '../../e2e/support/env'

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

it('uses a shell with line editing and bracketed paste, regardless of the runner login shell', () => {
  vi.stubEnv('SHELL', '/a/non-POSIX/login-shell')
  const env = cleanEnv('/temporary/omo-test', '/test/programs')
  if (process.platform === 'win32') {
    // Windows has no login-shell variable of its own: the harness leaves it alone and the terminal specs find Bash through PATH.
    expect(env.SHELL).toBe('/a/non-POSIX/login-shell')
  } else {
    expect(env.SHELL).toMatch(/(?:^|\/)bash$/)
  }
})

// Native absolute paths: the injected `executable` never touches the disk, so
// these checks hold on every platform, including drive-letter paths.
const at = (...parts: string[]) => resolve(sep, ...parts)

it('preserves compatible interactive shells and resolves Bash for other login shells', () => {
  for (const shell of [at('opt', 'bin', 'bash'), at('opt', 'bin', 'zsh')]) {
    expect(terminalShell(shell, at('tools'), (p) => p === shell)).toBe(shell)
  }
  expect(terminalShell(at('bin', 'fish'), [at('tools'), at('elsewhere')].join(delimiter), (p) => p === at('tools', 'bash'))).toBe(at('tools', 'bash'))
  expect(terminalShell(at('bin', 'sh'), '', (p) => p === '/bin/bash')).toBe('/bin/bash')
  expect(() => terminalShell(at('bin', 'fish'), '', () => false)).toThrow('Bash with line editing and bracketed paste')
})

it('finds Bash and git under their native executable names', () => {
  const dir = at('tools')
  expect(terminalShell(undefined, dir, (p) => p === join(dir, 'bash.exe'), 'win32')).toBe(join(dir, 'bash.exe'))
  expect(findGit(dir, 'win32', (p) => p === join(dir, 'git.exe'))).toBe(join(dir, 'git.exe'))
  expect(findGit(dir, 'linux', (p) => p === join(dir, 'git'))).toBe(join(dir, 'git'))
  expect(() => findGit(dir, 'win32', () => false)).toThrow('git is not on the PATH')
})

it('puts git into a program folder by link, or by a launcher where a link is refused', () => {
  const real = at('Git', 'cmd', 'git.exe')
  const made: string[] = []
  const link = (target: string, file: string) => { made.push(`link ${target} ${file}`) }
  const refuse = () => { throw Object.assign(new Error('operation not permitted'), { code: 'EPERM' }) }
  const write = (file: string, text: string) => { made.push(`write ${file} ${text}`) }
  expect(installGit(at('p'), real, 'linux', link, write)).toBe('git')
  expect(installGit(at('p'), real, 'win32', link, write)).toBe('git.exe')
  expect(installGit(at('p'), real, 'win32', refuse, write)).toBe('git.cmd')
  expect(made).toEqual([
    `link ${real} ${join(at('p'), 'git')}`,
    `link ${real} ${join(at('p'), 'git.exe')}`,
    `write ${join(at('p'), 'git.cmd')} @"${real}" %*\r\n`,
  ])
  // A refused link is only expected on Windows; on other systems it is a real error.
  expect(() => installGit(at('p'), real, 'linux', refuse, write)).toThrow('operation not permitted')
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

it('isolates real Bash startup code and Readline from hostile runner files', () => {
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


const browserRoot = resolve('e2e')
const browserSources = readdirSync(browserRoot, { recursive: true, encoding: 'utf8' })
  .filter((name) => name.endsWith('.ts') && resolve(browserRoot, name) !== resolve(browserRoot, 'support/env.ts'))

it.each(browserSources)('all browser child environments inherit through the central shell-config filter: %s', (name) => {
  const text = readFileSync(resolve(browserRoot, name), 'utf8')
  // Individual reads (PATH, CPU settings, etc.) are fine. Inheriting the
  // entire environment must go through withoutShellConfig or cleanEnv.
  expect(text, name).not.toMatch(/\bprocess\s*\.\s*env\b(?!\s*(?:\.|\[))/)
})

it('does not skip whole fixture tests for a platform; only single POSIX-only assertions are conditional', () => {
  // omo-fixture.spec.ts and this file cover the program folder, the shell
  // resolution and the environment boundary on every platform: they are pure,
  // or use native names and paths. A skip of a whole test would drop all of it.
  for (const file of [resolve('e2e/omo-fixture.spec.ts'), resolve('src/testing/omoEnv.test.ts')]) {
    const text = readFileSync(file, 'utf8')
    const gates = text.split('\n').filter((line) => /\b(?:it|test|describe)\s*\.\s*skip(?:If)?\s*\(/.test(line))
    expect(gates, file).toEqual([])
  }
})
