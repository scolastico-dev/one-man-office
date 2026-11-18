import { spawn } from 'node:child_process'

// What the standalone browser drivers hand their Go helper. It is an allowlist,
// never a copy of the runner's environment: no omo identity, no git or shell
// setting. Two groups are kept on purpose:
//  - what a program needs to start and find its tools (PATH, the home, the
//    Windows system root), and
//  - every temporary root the runner configured. The helper makes its homes and
//    folders below it (t.TempDir, the Go build's work folder), so dropping them
//    would send that state to the shared system folder whatever the parent
//    allotted. Names are compared as the platform does: case-insensitively on
//    Windows.
const CARRIED = ['PATH', 'HOME', 'USERPROFILE', 'LOCALAPPDATA', 'SystemRoot', 'TMPDIR', 'TMP', 'TEMP', 'GOTMPDIR']

/** The helper's environment from `base`, the carried names with the case they have there, then `extra` (the driver's own settings). */
export function fixtureEnv(base = process.env, extra = {}, platform = process.platform) {
  const fold = (name) => (platform === 'win32' ? name.toUpperCase() : name)
  const wanted = new Set(CARRIED.map(fold))
  const env = {}
  for (const [name, value] of Object.entries(base)) {
    if (value !== undefined && wanted.has(fold(name))) env[name] = value
  }
  return { ...env, CGO_ENABLED: '0', ...extra }
}

function kill(child) {
  try {
    // The helper runs under `go test`; its process group takes the test binary along.
    if (process.platform === 'win32') child.kill()
    else process.kill(-child.pid, 'SIGKILL')
  } catch {
    // Already gone.
  }
}

/**
 * Starts the helper and waits for the line it prints with its fixtures. Every way
 * this can fail (the program cannot start, it ends early, the line is not JSON)
 * ends the process and its group before the promise rejects.
 */
export async function startFixture({
  command = 'go',
  args = ['test', './internal/server', '-run', '^TestProxySubdomainBrowserFixture$', '-count=1', '-v', '-timeout', '80s'],
  cwd,
  env = fixtureEnv(),
  onSpawn,
} = {}) {
  const child = spawn(command, args, { cwd, env, detached: process.platform !== 'win32' })
  let output = ''
  child.stdout.on('data', (data) => { output += data })
  child.stderr.on('data', (data) => { output += data })
  const exited = new Promise((resolve) => {
    child.once('close', (code) => resolve(code))
    child.once('error', () => resolve(null))
  })
  if (child.pid !== undefined) onSpawn?.(child.pid)
  const prefix = 'PROXY_BROWSER_FIXTURES '
  let fixtures
  try {
    fixtures = await new Promise((resolve, reject) => {
      const look = () => {
        const line = output.split('\n').find((l) => l.startsWith(prefix))
        if (!line) return
        try {
          resolve(JSON.parse(line.slice(prefix.length)))
        } catch {
          reject(new Error(`the helper's fixtures line is not JSON: ${line}`))
        }
      }
      child.stdout.on('data', look)
      child.once('error', reject)
      child.once('close', (code) => reject(new Error(`fixture exited ${code}: ${output}`)))
    })
  } catch (error) {
    kill(child)
    await exited
    throw error
  }
  let stopped
  return {
    fixtures,
    output: () => output,
    stop(stopUrl) {
      stopped ??= (async () => {
        // The address belongs to the helper's own wrapper, never the omo API.
        const asked = stopUrl ? await fetch(stopUrl).then(() => true, () => false) : false
        if (!asked) kill(child)
        const code = await exited
        kill(child)
        return code
      })()
      return stopped
    },
  }
}
