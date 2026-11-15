import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { BINARY, REPO_DIR, SEED_BINARY, WEB_DIR } from './support/omo'

// Runs once before the suite: checks that the binary under test is built with
// the current app and builds the seeding tool.
export default function globalSetup(): void {
  const index = join(WEB_DIR, 'dist', 'index.html')
  if (!existsSync(BINARY)) throw new Error(`${BINARY} does not exist: run make build first`)
  if (!existsSync(index)) throw new Error(`${index} does not exist: run make build first`)
  if (statSync(BINARY).mtimeMs < statSync(index).mtimeMs) {
    throw new Error(`${BINARY} is older than the app in web/dist: run make build first`)
  }
  mkdirSync(dirname(SEED_BINARY), { recursive: true })
  execFileSync('go', ['build', '-o', SEED_BINARY, './web/e2e/seed'], {
    cwd: REPO_DIR,
    stdio: 'inherit',
    env: { ...process.env, CGO_ENABLED: '0' },
  })
}
