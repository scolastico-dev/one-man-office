import { execFileSync } from 'node:child_process'
import { mkdirSync } from 'node:fs'
import { devNull } from 'node:os'

/** Runs Git in a test-owned folder without inherited Git configuration. */
export function git(cwd: string, ...args: string[]): Buffer {
  const env: NodeJS.ProcessEnv = {}
  for (const [key, value] of Object.entries(process.env)) {
    const upper = key.toUpperCase()
    if (upper.startsWith('GIT_CONFIG') || upper.startsWith('OMO_')) continue
    env[key] = value
  }
  env.GIT_CONFIG_GLOBAL = devNull
  env.GIT_CONFIG_SYSTEM = devNull
  return execFileSync('git', args, { cwd, env, stdio: 'pipe' })
}

/**
 * Makes `dir` a git repository on `main` with one empty commit. The identity
 * and the signing switch are given per command, so nothing of the developer's
 * git configuration is read or written.
 */
export function initRepo(dir: string): void {
  mkdirSync(dir, { recursive: true })
  git(dir, 'init', '-q', '-b', 'main')
  git(dir, '-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '--allow-empty', '-m', 'init')
}
