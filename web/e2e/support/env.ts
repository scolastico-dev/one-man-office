import { accessSync, constants, statSync } from 'node:fs'
import { devNull } from 'node:os'
import { basename, delimiter, isAbsolute, join, resolve } from 'node:path'

/** The agent programs omo knows; the real ones of the developer's machine must never be reachable from a test's omo (a first start or a usage script would run them). */
const AGENT_PROGRAMS = ['claude', 'codex', 'agy', 'gemini'] as const

// HOME alone does not isolate shell startup files, Readline, imported options
// or the line editor's mode. These are runner settings, never test settings.
const SHELL_OVERRIDES = new Set([
  'ENV', 'INPUTRC', 'ZDOTDIR', 'SHELLOPTS', 'BASHOPTS', 'POSIXLY_CORRECT',
  'EDITOR', 'VISUAL', 'CDPATH', 'GLOBIGNORE', 'FIGNORE', 'KEYTIMEOUT', 'SAVEHIST',
])

function shellOverride(key: string): boolean {
  return SHELL_OVERRIDES.has(key) || key.startsWith('BASH_')
    || key.startsWith('HIST') || key.startsWith('ZLE_')
    || /^(?:PS\d+|RPS\d+|PSVAR|PROMPT.*|RPROMPT.*|SPROMPT)$/.test(key)
}

/** Every browser child drops runner shell config; a spec may overlay its own. */
export function withoutShellConfig(base: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {}
  for (const [key, value] of Object.entries(base)) {
    if (!shellOverride(key.toUpperCase())) env[key] = value
  }
  // Do not fall back to a runner's machine-wide /etc/inputrc either.
  return { ...env, INPUTRC: devNull }
}

export function holdsAgentProgram(dir: string): boolean {
  const exts = process.platform === 'win32' ? ['', '.exe', '.cmd', '.bat'] : ['']
  return AGENT_PROGRAMS.some((p) =>
    exts.some((e) => {
      try {
        accessSync(join(dir, p + e), constants.X_OK)
        return true
      } catch {
        return false
      }
    }),
  )
}

/** The runner's own PATH without every folder that holds an agent program or a shim of one. */
export function pathWithoutAgentPrograms(path: string | undefined = process.env.PATH): string {
  return (path ?? '').split(delimiter).filter((dir) => dir !== '' && !holdsAgentProgram(dir)).join(delimiter)
}

function isExecutable(file: string): boolean {
  try {
    accessSync(file, constants.X_OK)
    return statSync(file).isFile()
  } catch {
    return false
  }
}

/** POSIX syntax plus interactive line editing and bracketed paste, including on macOS. */
export function terminalShell(inherited: string | undefined, path: string | undefined, executable = isExecutable): string {
  if (inherited && isAbsolute(inherited) && ['bash', 'zsh'].includes(basename(inherited)) && executable(inherited)) return inherited
  for (const dir of (path ?? '').split(delimiter).filter(Boolean)) {
    const bash = resolve(dir, 'bash')
    if (executable(bash)) return bash
  }
  if (executable('/bin/bash')) return '/bin/bash'
  throw new Error('Browser terminal tests require Bash with line editing and bracketed paste; install Bash or set SHELL to an executable Bash or Zsh')
}

/** An environment that cannot reach anything of the developer's omo, git or shell session; `path` replaces PATH when given. */
export function cleanEnv(home: string, path?: string): NodeJS.ProcessEnv {
  const cleanPath = path ?? pathWithoutAgentPrograms()
  const env: NodeJS.ProcessEnv = {}
  for (const [k, v] of Object.entries(withoutShellConfig())) {
    const key = k.toUpperCase()
    if (key.startsWith('OMO_') || key === 'APPDATA' || key === 'USERPROFILE' || key.startsWith('XDG_') || key.startsWith('GIT_CONFIG')) continue
    env[k] = v
  }
  return {
    ...env,
    // omo's own git (merges, squashes) and the agents' git read no global or system configuration: no developer signing setup.
    GIT_CONFIG_GLOBAL: devNull,
    GIT_CONFIG_NOSYSTEM: '1',
    OMO_HOME: join(home, 'omo'),
    HOME: home,
    USERPROFILE: home,
    APPDATA: join(home, 'appdata'),
    XDG_CONFIG_HOME: join(home, 'xdg-config'),
    XDG_STATE_HOME: join(home, 'xdg-state'),
    XDG_DATA_HOME: join(home, 'xdg-data'),
    // Shell specs need POSIX commands, line editing and bracketed paste. /bin/sh
    // may be dash or Bash in POSIX mode; the runner may also log in with fish.
    ...(process.platform === 'win32' ? {} : { SHELL: terminalShell(process.env.SHELL, cleanPath) }),
    // omo's plugin list: a repository at this place in the test's home (see
    // pluginListPath), never the real one on the network.
    OMO_PLUGIN_CATALOG: pluginListPath(home),
    // Without a PATH of the test's own, the runner's PATH minus the folders of the real agent programs.
    [process.platform === 'win32' ? 'Path' : 'PATH']: cleanPath,
  }
}

/** Where omo reads its plugin list in a test: a bare repository in the test's temporary folder, missing until a test makes it (`makePluginList`). */
export function pluginListPath(home: string): string {
  return join(home, 'plugin-list.git')
}
