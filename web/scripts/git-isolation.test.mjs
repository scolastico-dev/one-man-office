import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'

const helper = new URL('../e2e/support/git.ts', import.meta.url).href

for (const source of ['global', 'system', 'count', 'parameters']) {
  test(`browser Git helper ignores inherited ${source} configuration`, () => {
    const root = mkdtempSync(join(tmpdir(), 'omo-git-isolation-'))
    try {
      const template = join(root, 'template')
      mkdirSync(template)
      writeFileSync(join(template, 'isolation-marker'), 'inherited configuration was used')
      const config = join(root, 'gitconfig')
      // Git configuration accepts forward slashes on Windows too.
      const value = template.replaceAll('\\', '/')
      writeFileSync(config, `[init]\n\ttemplateDir = "${value}"\n`)
      // Only this child's synthetic environment is contaminated. The runner's
      // session variables and real configuration are never read or changed.
      const env = {
        PATH: process.env.PATH,
        SystemRoot: process.env.SystemRoot,
        HOME: root,
        USERPROFILE: root,
        XDG_CONFIG_HOME: root,
      }
      if (source === 'global') env.GIT_CONFIG_GLOBAL = config
      if (source === 'system') env.GIT_CONFIG_SYSTEM = config
      if (source === 'count') {
        env.GIT_CONFIG_COUNT = '1'
        env.GIT_CONFIG_KEY_0 = 'init.templateDir'
        env.GIT_CONFIG_VALUE_0 = value
      }
      if (source === 'parameters') env.GIT_CONFIG_PARAMETERS = `'init.templateDir=${value.replaceAll("'", "'\\''")}'`
      const child = spawnSync(process.execPath, ['--input-type=module', '-e', `
        import assert from 'node:assert/strict'
        import { existsSync, readFileSync } from 'node:fs'
        import { join } from 'node:path'
        import { git, initRepo } from ${JSON.stringify(helper)}
        const repo = join(process.env.HOME, 'repo')
        initRepo(repo)
        assert.equal(existsSync(join(repo, '.git', 'isolation-marker')), false, 'Git copied the inherited template')
        assert.equal(readFileSync(join(repo, '.git', 'HEAD'), 'utf8').trim(), 'ref: refs/heads/main')
        const worktree = join(process.env.HOME, 'worktree')
        git(repo, 'worktree', 'add', '-b', 'reviewed', worktree, 'main')
        assert.equal(git(worktree, 'branch', '--show-current').toString().trim(), 'reviewed')
      `], { env, encoding: 'utf8' })
      assert.equal(child.status, 0, child.stderr || String(child.error ?? 'child failed'))
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  })
}
