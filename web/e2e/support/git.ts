import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import { devNull } from 'node:os'
import { withoutShellConfig } from './env'

/**
 * Makes `dir` a git repository on `main` with one empty commit. The identity
 * and the signing switch are given per command, so nothing of the developer's
 * git configuration is read or written.
 */
export function initRepo(dir: string): void {
  mkdirSync(dir, { recursive: true })
  const env = { ...withoutShellConfig(), GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
  const git = (...args: string[]) => execFileSync('git', args, { cwd: dir, env, stdio: 'pipe' })
  git('init', '-q', '-b', 'main')
  git('-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '--allow-empty', '-m', 'init')
}
