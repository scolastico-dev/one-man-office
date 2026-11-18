import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, realpathSync, rmSync, symlinkSync } from 'node:fs'
import { devNull, tmpdir } from 'node:os'
import { dirname, join, relative, resolve } from 'node:path'
import { initRepo } from './git'
import { withoutShellConfig } from './env'

/**
 * What a path below a workspace folder is: a git repository, a plain folder,
 * a linked worktree of a repository of the same layout (`{ worktree: 'a/one' }`,
 * its `.git` is a file), or a link to another path of the layout
 * (`{ link: 'a' }`).
 */
export type WorkspaceEntry = 'repo' | 'dir' | { worktree: string } | { link: string }

export interface Workspace {
  /** The workspace folder, a real path (no link in it). */
  root: string
  /** Absolute path of an entry of the layout. */
  path(rel: string): string
  cleanup(): void
}

/**
 * Builds a workspace folder from `layout` (slash paths below it) inside
 * `under`, which must be a folder of the test's own temporary home: a test
 * never builds anything below the real home or workspace. Repositories come
 * first, then worktrees, then links, so each can refer to the others.
 */
export function makeWorkspace(layout: Record<string, WorkspaceEntry>, under: string): Workspace {
  const tmp = realpathSync(tmpdir())
  const base = realpathSync(under)
  if (!base.startsWith(tmp + '/')) throw new Error(`${base} is not below the temporary folder ${tmp}`)
  const root = mkdtempSync(join(base, 'workspace-'))
  const at = (rel: string) => {
    const p = resolve(root, rel)
    if (!p.startsWith(root + '/')) throw new Error(`${rel} leaves the workspace folder`)
    return p
  }
  const entries = Object.entries(layout)
  for (const [rel, kind] of entries) {
    if (kind === 'repo') initRepo(at(rel))
    else if (kind === 'dir') mkdirSync(at(rel), { recursive: true })
  }
  const env = { ...withoutShellConfig(), GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
  let worktrees = 0
  for (const [rel, kind] of entries) {
    if (typeof kind !== 'object' || !('worktree' in kind)) continue
    mkdirSync(dirname(at(rel)), { recursive: true })
    execFileSync('git', ['worktree', 'add', '-q', '-b', `e2e-worktree-${++worktrees}`, at(rel)], {
      cwd: at(kind.worktree),
      env,
      stdio: 'pipe',
    })
  }
  for (const [rel, kind] of entries) {
    if (typeof kind !== 'object' || !('link' in kind)) continue
    mkdirSync(dirname(at(rel)), { recursive: true })
    symlinkSync(relative(dirname(at(rel)), at(kind.link)), at(rel), 'dir')
  }
  return {
    root,
    path: at,
    cleanup: () => rmSync(root, { recursive: true, force: true }),
  }
}
