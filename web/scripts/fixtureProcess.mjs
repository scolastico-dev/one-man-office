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
 * A test that replaces one platform's kill step must say which platform it replaces: with the host left to choose, the
 * other platforms would never run the step it injected. Refused wherever the options come from.
 */
export function checkEndOptions(options = {}) {
  if (options.platform === undefined && (options.killGroup !== undefined || options.killTree !== undefined)) {
    throw new Error('end.killGroup and end.killTree replace one platform\'s step: give end.platform too, or use end.kill, which replaces the step on every platform')
  }
}

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
 *
 * Test seams: `kill(child, { tree })` replaces the whole platform step, so a test
 * that makes the kill fail or holds it back runs the same branch on every
 * platform. `killTree` and `killGroup` replace one platform's step only, and are
 * for a test that also sets `platform`, never for one that leaves it to the host.
 */
export function endProcess(child, options = {}) {
  checkEndOptions(options)
  const { tree = false, platform = process.platform, killTree = taskkill, killGroup = (pid) => process.kill(-pid, 'SIGKILL'), kill } = options
  if (child.pid === undefined || exited(child)) return
  if (kill) {
    kill(child, { tree })
    return
  }
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
// folder behind. Each running fixture registers a cleanup here: `teardown` ends the
// process, waits for its close and removes the folder; `quick` is the synchronous
// best effort. The signal handlers exist only while one is registered.
const running = new Set()
const SIGNALS = ['SIGINT', 'SIGTERM']
let interrupted = false
function reportCleanup(error) {
  process.stderr.write(`fixture cleanup failed: ${error?.stack ?? error}\n`)
}
// The last resort at process exit, where Node cannot wait: the kill is sent and the folder is removed at once. On
// Windows a still-running .exe cannot be deleted, so this path can leave the folder behind (reported on stderr);
// the signal path below is the one that waits for the close.
function exitCleanup() {
  for (const entry of [...running]) {
    try {
      entry.quick()
    } catch (error) {
      reportCleanup(error)
    }
  }
}
async function onSignal(signal) {
  if (interrupted) {
    // A second signal while the first is being handled: stop waiting.
    exitCleanup()
  } else {
    interrupted = true
    const results = await Promise.allSettled([...running].map((entry) => entry.teardown()))
    for (const result of results) if (result.status === 'rejected') reportCleanup(result.reason)
  }
  unwatchAll()
  interrupted = false
  process.kill(process.pid, signal) // the default action now ends this process with the signal it was sent
}
function watch(entry) {
  if (running.size === 0) {
    for (const signal of SIGNALS) process.on(signal, onSignal)
    process.on('exit', exitCleanup)
  }
  running.add(entry)
}
function unwatchAll() {
  for (const signal of SIGNALS) process.off(signal, onSignal)
  process.off('exit', exitCleanup)
}
function unwatch(entry) {
  running.delete(entry)
  if (running.size === 0) unwatchAll()
}

// A program that starts other programs: never the helper itself (the child held must be the process that owns the servers).
const LAUNCHERS = new Set(['go', 'npm', 'npx', 'pnpm', 'yarn', 'sh', 'bash', 'zsh', 'dash', 'cmd', 'powershell', 'pwsh'])
/** True for a program that launches others (`go`, `npm`, `npx`, a shell), by its file name without folder or Windows suffix. */
export function isLauncher(command) {
  const name = String(command).split(/[\\/]/).pop().toLowerCase().replace(/\.(exe|cmd|bat|com)$/, '')
  return LAUNCHERS.has(name)
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
  end = {},
  remove = (folder) => rmSync(folder, { recursive: true, force: true }),
  onSpawn,
  onOutput,
} = {}) {
  if (command !== undefined && isLauncher(command)) {
    throw new Error(`the helper must be the program that owns the servers, not the launcher ${command}: build a binary and run it directly`)
  }
  checkEndOptions(end)
  const root = cwd instanceof URL ? fileURLToPath(cwd) : cwd
  let dir
  let current // the process that is running now: the build, then the helper
  // Ends what runs and waits for its close, then removes the folder. Always runs both steps, whatever the first does.
  // The folder is removed through `remove` (a test seam; default `rmSync`). A failure to end the process and a failure to remove
  // the folder are both reported, together when both happen: the folder of a process that could not be ended may be impossible
  // to delete (Windows cannot delete a running executable), and that must come out as a message, never as a folder left unseen.
  const removeDir = () => {
    try {
      remove(dir)
    } catch (error) {
      throw new Error(`could not remove the build folder ${dir}: ${error.message}`, { cause: error })
    }
  }
  const teardown = async () => {
    const errors = []
    try {
      if (current) {
        endProcess(current.child, { tree: current.tree, ...end })
        await current.ended
      }
    } catch (error) {
      errors.push(error)
    }
    unwatch(entry)
    try {
      if (dir) removeDir()
    } catch (error) {
      errors.push(error)
    }
    if (errors.length === 1) throw errors[0]
    if (errors.length > 1) throw new AggregateError(errors, errors.map((error) => error.message).join('\nand '))
  }
  const entry = {
    teardown,
    quick() {
      if (current) endProcess(current.child, { tree: current.tree, ...end })
      if (dir) removeDir()
    },
  }
  watch(entry)
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
        let code
        try {
          // The address belongs to the helper's own wrapper, never the omo API.
          const asked = stopUrl ? await fetch(stopUrl).then(() => true, () => false) : false
          if (!asked) endProcess(child, end)
          code = await exitedHelper
        } catch (error) {
          return fail(error) // the folder is removed and the handlers are released all the same
        }
        await teardown()
        return code
      })()
      return stopped
    },
  }
}
