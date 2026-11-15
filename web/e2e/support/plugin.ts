import { execFileSync } from 'node:child_process'
import { mkdirSync, realpathSync, writeFileSync } from 'node:fs'
import { devNull, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { initRepo } from './git'

const GIT_ENV = { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
const git = (cwd: string, ...args: string[]) =>
  execFileSync('git', ['-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', ...args], { cwd, env: GIT_ENV, stdio: 'pipe' })
    .toString()
    .trim()

export interface PluginRepo {
  /** The bare repository omo installs from: an absolute local path in the test's temporary folder. */
  source: string
  /** The commit the bare repository's main points at. */
  head(): string
  /** Writes the manifest and files again (only those given), commits and pushes; answers the new commit. */
  commit(manifest: object | null, files?: Record<string, string>): string
}

/**
 * Writes a plugin repository (`plugin.json` from `manifest`, plus `files` by
 * relative path) inside `dir`, which must be a folder of the test's own
 * temporary home, commits it and pushes it to a bare repository next to it.
 * Nothing outside the temporary folder is read or written, and no network
 * address is used.
 */
export function makePlugin(dir: string, manifest: object, files: Record<string, string> = {}): PluginRepo {
  const tmp = realpathSync(tmpdir())
  mkdirSync(dir, { recursive: true })
  const root = realpathSync(dir)
  if (!root.startsWith(tmp + '/')) throw new Error(`${root} is not below the temporary folder ${tmp}`)
  const work = join(root, 'work')
  const bare = join(root, 'plugin.git')
  initRepo(work)
  mkdirSync(bare, { recursive: true })
  git(bare, 'init', '--bare', '-q', '-b', 'main')
  const write = (m: object | null, f: Record<string, string>) => {
    if (m !== null) writeFileSync(join(work, 'plugin.json'), JSON.stringify(m, null, 2))
    for (const [rel, text] of Object.entries(f)) {
      const p = resolve(work, rel)
      if (!p.startsWith(work + '/')) throw new Error(`${rel} leaves the plugin folder`)
      mkdirSync(dirname(p), { recursive: true })
      writeFileSync(p, text)
    }
  }
  const repo: PluginRepo = {
    source: bare,
    head: () => git(bare, 'rev-parse', 'main'),
    commit(m, f = {}) {
      write(m, f)
      git(work, 'add', '--', ...(m === null ? [] : ['plugin.json']), ...Object.keys(f))
      git(work, 'commit', '-q', '-m', 'plugin')
      git(work, 'push', '-q', bare, 'main')
      return git(work, 'rev-parse', 'HEAD')
    },
  }
  repo.commit(manifest, files)
  return repo
}
