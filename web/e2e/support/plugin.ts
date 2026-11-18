import { execFileSync } from 'node:child_process'
import { appendFileSync, cpSync, mkdirSync, realpathSync, writeFileSync } from 'node:fs'
import { devNull, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { initRepo } from './git'
import { REPO_DIR } from './omo'
import { withoutShellConfig } from './env'

const GIT_ENV = { ...withoutShellConfig(), GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
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

/** An entry of omo's plugin list that a test adds after the repository's own. */
export interface ListEntry {
  name: string
  tag: 'official' | 'recommended' | 'community'
  description: string
  source?: string
  ref?: string
  path?: string
}

/**
 * Makes the bare repository `bare` (below the temporary folder) a stand-in
 * for the release branch of omo's repository: this checkout's `plugins`
 * folder (its list and its official plugins) on `main`, with `extra`
 * entries added to the list. Answers the folder of the work tree.
 */
export function makePluginList(bare: string, extra: ListEntry[] = []): string {
  const tmp = realpathSync(tmpdir())
  mkdirSync(dirname(bare), { recursive: true })
  const root = realpathSync(dirname(bare))
  if (!root.startsWith(tmp + '/')) throw new Error(`${root} is not below the temporary folder ${tmp}`)
  const work = join(root, 'plugin-list-work')
  initRepo(work)
  cpSync(join(REPO_DIR, 'plugins'), join(work, 'plugins'), { recursive: true })
  for (const e of extra) {
    const lines = [`  - name: ${e.name}`, `    tag: ${e.tag}`, `    description: ${JSON.stringify(e.description)}`]
    if (e.source !== undefined) lines.push(`    source: ${JSON.stringify(e.source)}`)
    if (e.ref !== undefined) lines.push(`    ref: ${JSON.stringify(e.ref)}`)
    if (e.path !== undefined) lines.push(`    path: ${JSON.stringify(e.path)}`)
    appendFileSync(join(work, 'plugins', 'index.yaml'), lines.join('\n') + '\n')
  }
  git(work, 'add', '--', 'plugins')
  git(work, 'commit', '-q', '-m', 'plugin list')
  mkdirSync(bare, { recursive: true })
  git(bare, 'init', '--bare', '-q', '-b', 'main')
  git(work, 'push', '-q', bare, 'main')
  return work
}
