import { spawn, spawnSync } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

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

/** The first complete line of `output` (ended by a newline) that starts with `prefix`, without its line end. A pipe write may arrive in pieces, so the unterminated tail is never a line yet. */
export function completeLine(output, prefix) {
  const lines = output.split('\n')
  lines.pop()
  return lines.map((line) => line.replace(/\r$/, '')).find((line) => line.startsWith(prefix))
}

/** The arguments of the Windows command that ends a process and everything it started. */
export function taskkillArgs(pid) {
  return ['/PID', String(pid), '/T', '/F']
}

function taskkill(pid) {
  const result = spawnSync('taskkill', taskkillArgs(pid), { encoding: 'utf8', windowsHide: true })
  if (result.error || result.status !== 0) {
    throw new Error(`taskkill of process ${pid} failed (${result.error?.message ?? `exit ${result.status}`}): ${result.stdout}${result.stderr}`)
  }
}

const exited = (child) => child.exitCode !== null || child.signalCode !== null

/**
 * Ends a process that is still running, without waiting; the caller waits for its
 * close. A process that has ended (or never started) needs nothing. A failure to
 * end a running one is an error, never "already gone":
 *  - POSIX: the child leads its own process group (`detached`), and the group is
 *    killed; only ESRCH (nothing left in the group) is no failure.
 *  - Windows, a program the helper owns directly: `child.kill`. Node answers
 *    false only for a process that is already gone and reports every other
 *    failure as the child's `error` event, which `launch` turns into a rejection.
 *  - Windows, a program that starts other programs itself (`tree`, the Go build:
 *    the go command runs compilers and linker): `taskkill /T /F`, which a
 *    Windows `child.kill` cannot replace because it ends the go command alone.
 */
export function endProcess(child, { tree = false, platform = process.platform, killTree = taskkill, killGroup = (pid) => process.kill(-pid, 'SIGKILL') } = {}) {
  if (child.pid === undefined || exited(child)) return
  if (platform === 'win32') {
    if (!tree) {
      child.kill('SIGKILL')
      return
    }
    try {
      killTree(child.pid)
    } catch (error) {
      if (!exited(child)) throw error
    }
    return
  }
  try {
    killGroup(child.pid)
  } catch (error) {
    if (error.code !== 'ESRCH') throw error
  }
}

/** Starts a program as the leader of its own process group; `ended` answers its exit code once it is closed and rejects when it cannot be started or controlled. */
function launch(command, args, { cwd, env, tree = false }) {
  const child = spawn(command, args, { cwd, env, detached: process.platform !== 'win32', windowsHide: true })
  const ended = new Promise((resolveEnded, rejectEnded) => {
    child.once('close', (code) => resolveEnded(code))
    child.once('error', (error) => {
      rejectEnded(child.pid === undefined ? error : new Error(`process ${child.pid} could not be controlled: ${error.message}`))
    })
  })
  ended.catch(() => {}) // a caller that never awaits it must not leave an unhandled rejection; awaiting callers still see it
  return { child, ended, tree }
}

// A driver that is interrupted (or crashes) must not leave its helper or build
// folder behind. Each running fixture registers a synchronous cleanup here; the
// signal handlers exist only while one is registered.
const cleanups = new Set()
const SIGNALS = ['SIGINT', 'SIGTERM']
function runCleanups() {
  for (const cleanup of [...cleanups]) {
    try {
      cleanup()
    } catch (error) {
      process.stderr.write(`fixture cleanup failed: ${error.stack ?? error}\n`)
    }
  }
}
function onSignal(signal) {
  runCleanups()
  unwatch()
  process.kill(process.pid, signal) // the default action now ends this process with the signal it was sent
}
function watch(cleanup) {
  if (cleanups.size === 0) {
    for (const signal of SIGNALS) process.on(signal, onSignal)
    process.on('exit', runCleanups)
  }
  cleanups.add(cleanup)
}
function unwatch(cleanup) {
  if (cleanup) cleanups.delete(cleanup)
  if (cleanups.size === 0 || !cleanup) {
    for (const signal of SIGNALS) process.off(signal, onSignal)
    process.off('exit', runCleanups)
  }
}

const HELPER_PACKAGE = './internal/server'
const HELPER_ARGS = ['-test.run=^TestProxySubdomainBrowserFixture$', '-test.count=1', '-test.v', '-test.timeout=80s']

/**
 * Starts the helper and waits for the line it prints with its fixtures.
 *
 * Without a `command` the helper is the Go test binary of `build.pkg` (default
 * the server package, below `cwd`, the repository root), built once into a folder
 * of its own under `tmp` and started directly: the child held here is the process
 * that owns the servers, with no `go test` in between, so ending it ends them on
 * every platform. The folder is removed when the helper is closed.
 *
 * Every way this can fail (the build, the program cannot start, it ends early, the
 * line is not JSON) and every stop waits for the process to be closed and for the
 * folder to be removed before the promise settles; a failure to end the process
 * or remove the folder is an error of its own. An interrupt of the driver ends
 * the helper and removes the folder before the driver itself ends.
 */
export async function startFixture({
  command,
  args,
  build = {},
  cwd,
  env = fixtureEnv(),
  tmp = tmpdir(),
  onSpawn,
  onOutput,
} = {}) {
  const root = cwd instanceof URL ? fileURLToPath(cwd) : cwd
  let dir
  let current // the process that is running now: the build, then the helper
  const cleanup = () => {
    if (current) endProcess(current.child, { tree: current.tree })
    if (dir) rmSync(dir, { recursive: true, force: true })
  }
  watch(cleanup)
  // Ends what runs and waits for its close, then removes the folder. Always runs both steps.
  const teardown = async () => {
    try {
      if (current) {
        endProcess(current.child, { tree: current.tree })
        await current.ended
      }
    } finally {
      unwatch(cleanup)
      if (dir) rmSync(dir, { recursive: true, force: true })
    }
  }
  const fail = async (error) => {
    try {
      await teardown()
    } catch (second) {
      throw new AggregateError([error, second], `${error.message}\nand the cleanup failed: ${second.message}`)
    }
    throw error
  }

  let workdir = root
  if (command === undefined) {
    const pkg = build.pkg ?? HELPER_PACKAGE
    try {
      dir = mkdtempSync(join(tmp, 'omo-fixture-'))
    } catch (error) {
      return fail(error)
    }
    command = join(dir, process.platform === 'win32' ? 'fixture.exe' : 'fixture')
    args ??= HELPER_ARGS
    workdir = root === undefined ? undefined : resolve(root, pkg)
    let built = ''
    try {
      current = launch('go', ['test', '-c', '-o', command, pkg], { cwd: root, env, tree: true })
      const collect = (data) => { built += data }
      current.child.stdout.on('data', collect)
      current.child.stderr.on('data', collect)
      const code = await current.ended
      if (code !== 0) throw new Error(`building the helper failed (${code}): ${built}`)
      current = undefined
    } catch (error) {
      return fail(error)
    }
  }

  current = launch(command, args ?? [], { cwd: workdir, env })
  const child = current.child
  const exitedHelper = current.ended
  let output = ''
  const send = (text) => { child.stdin.write(text) }
  const collect = (data) => { output += data; onOutput?.(output, send) }
  child.stdout.on('data', collect)
  child.stderr.on('data', collect)
  child.stdin.on('error', () => {})
  if (child.pid !== undefined) onSpawn?.(child.pid)
  const prefix = 'PROXY_BROWSER_FIXTURES '
  let fixtures
  try {
    fixtures = await new Promise((resolveFixtures, reject) => {
      const look = () => {
        const line = completeLine(output, prefix)
        if (!line) return
        try {
          resolveFixtures(JSON.parse(line.slice(prefix.length)))
        } catch {
          reject(new Error(`the helper's fixtures line is not JSON: ${line}`))
        }
      }
      child.stdout.on('data', look)
      child.once('error', reject)
      child.once('close', (code) => reject(new Error(`fixture exited ${code}: ${output}`)))
    })
  } catch (error) {
    return fail(error)
  }
  let stopped
  return {
    fixtures,
    output: () => output,
    stop(stopUrl) {
      stopped ??= (async () => {
        // The address belongs to the helper's own wrapper, never the omo API.
        const asked = stopUrl ? await fetch(stopUrl).then(() => true, () => false) : false
        if (!asked) endProcess(child)
        try {
          return await exitedHelper
        } finally {
          current = undefined
          unwatch(cleanup)
          if (dir) rmSync(dir, { recursive: true, force: true })
        }
      })()
      return stopped
    },
  }
}
