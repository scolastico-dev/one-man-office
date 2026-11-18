import { accessSync, constants } from 'node:fs'
import { devNull } from 'node:os'
import { delimiter, join } from 'node:path'

/** The agent programs omo knows; the real ones of the developer's machine must never be reachable from a test's omo (a first start or a usage script would run them). */
const AGENT_PROGRAMS = ['claude', 'codex', 'agy', 'gemini'] as const

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

/** An environment that cannot reach anything of the developer's omo, git or shell session; `path` replaces PATH when given. */
export function cleanEnv(home: string, path?: string): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {}
  for (const [k, v] of Object.entries(process.env)) {
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
    // omo's plugin list: a repository at this place in the test's home (see
    // pluginListPath), never the real one on the network.
    OMO_PLUGIN_CATALOG: pluginListPath(home),
    // Without a PATH of the test's own, the runner's PATH minus the folders of the real agent programs.
    [process.platform === 'win32' ? 'Path' : 'PATH']: path ?? pathWithoutAgentPrograms(),
  }
}

/** Where omo reads its plugin list in a test: a bare repository in the test's temporary folder, missing until a test makes it (`makePluginList`). */
export function pluginListPath(home: string): string {
  return join(home, 'plugin-list.git')
}
