// @vitest-environment node
import { spawn, spawnSync } from 'node:child_process'
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { delimiter, join, resolve } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { completeLine, endProcess, fixtureEnv, isLauncher, startFixture, taskkillArgs } from '../../scripts/fixtureProcess.mjs'

// An owned temporary root that is not below the shared one and has a space in it.
const owned = resolve('owned temporary root', 'tmp')
const roots = ['TMPDIR', 'TMP', 'TEMP', 'GOTMPDIR']

function alive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

describe('the environment of the standalone browser drivers\' helper process', () => {
  it('carries every configured temporary root unchanged, spaces included', () => {
    const base = Object.fromEntries(roots.map((key) => [key, owned]))
    const env = fixtureEnv({ ...base, PATH: ['/a', '/b'].join(delimiter) })
    for (const key of roots) expect(env[key], key).toBe(owned)
  })

  it('lets a child allocate below the configured root, not the shared one', () => {
    const env = fixtureEnv({ PATH: process.env.PATH, SystemRoot: process.env.SystemRoot, TMPDIR: owned, TMP: owned, TEMP: owned })
    const seen = spawnSync(process.execPath, ['-e', 'process.stdout.write(require("node:os").tmpdir())'], { env, encoding: 'utf8' })
    expect(seen.error).toBeUndefined()
    expect(seen.stdout).toBe(owned)
  })

  it('stays an allowlist: no omo identity, no shell setting, no helper activation unless asked', () => {
    const env = fixtureEnv({ PATH: '/p', HOME: '/h', OMO_SOCKET: '/live.sock', omo_agent_id: 'x', EDITOR: 'vi', PROXY_BROWSER_FIXTURE: '1', INPUTRC: '/x' })
    expect(Object.keys(env).sort()).toEqual(['CGO_ENABLED', 'HOME', 'PATH'])
    expect(env.CGO_ENABLED).toBe('0')
    expect(fixtureEnv({ PATH: '/p' }, { PROXY_BROWSER_FIXTURE: '1' }).PROXY_BROWSER_FIXTURE).toBe('1')
  })

  it('finds Windows names in any case and keeps the case it found', () => {
    const env = fixtureEnv({ Path: 'C:\\x', temp: 'C:\\owned root', SystemRoot: 'C:\\Windows' }, {}, 'win32')
    expect(env).toEqual({ Path: 'C:\\x', temp: 'C:\\owned root', SystemRoot: 'C:\\Windows', CGO_ENABLED: '0' })
  })
})

describe('starting the helper process', () => {
  // A stand-in that prints one line and then waits for its input to close, as the Go helper waits for its stop request.
  const standIn = (line: string) => ['-e', `process.stdout.write(${JSON.stringify(line + '\n')}); process.stdin.resume()`]

  it('answers what the helper printed and ends it on stop', async () => {
    let pid = 0
    const run = await startFixture({ command: process.execPath, args: standIn('PROXY_BROWSER_FIXTURES [{"board":"b"}]'), onSpawn: (p) => { pid = p } })
    expect(run.fixtures).toEqual([{ board: 'b' }])
    expect(alive(pid)).toBe(true)
    // No stop address answers: the helper is ended, which has no exit code.
    expect(await run.stop()).toBeNull()
    expect(alive(pid)).toBe(false)
    expect(await run.stop()).toBeNull()
  })

  it('asks the helper to finish through its stop address and answers its exit code', async () => {
    // Like the Go helper: serves a stop address, and ends by itself with code 0 once it was asked.
    const script = `
      const server = require('node:http').createServer((request, response) => response.end('ok', () => process.exit(0)))
      server.listen(0, '127.0.0.1', () => process.stdout.write('PROXY_BROWSER_FIXTURES ' + JSON.stringify([{ stop: 'http://127.0.0.1:' + server.address().port + '/stop' }]) + '\\n'))`
    const run = await startFixture({ command: process.execPath, args: ['-e', script] })
    expect(await run.stop(run.fixtures[0]!.stop)).toBe(0)
  })

  it('ends the helper when its fixtures line cannot be read', async () => {
    let pid = 0
    await expect(startFixture({ command: process.execPath, args: standIn('PROXY_BROWSER_FIXTURES not-json'), onSpawn: (p) => { pid = p } })).rejects.toThrow(/fixtures/i)
    expect(pid).toBeGreaterThan(0)
    expect(alive(pid)).toBe(false)
  })

  it('reports the output when the helper ends before it printed its fixtures', async () => {
    await expect(startFixture({ command: process.execPath, args: ['-e', 'console.log("no fixtures"); process.exit(3)'] })).rejects.toThrow(/exited 3.*no fixtures/s)
  })

  it('reports a helper that cannot be started', async () => {
    await expect(startFixture({ command: 'omo-no-such-program-for-the-test' })).rejects.toThrow(/ENOENT/)
  })
})

describe('the standalone browser drivers', () => {
  const dir = resolve('scripts')
  const drivers = readdirSync(dir).filter((name) => name.endsWith('.test.mjs'))

  it('are found', () => {
    expect(drivers).toEqual(expect.arrayContaining(['proxy-bootstrap.test.mjs', 'proxy-subdomain.test.mjs']))
  })

  // A driver that wrote its own child environment could drop the configured temporary roots again;
  // starting a child process is fixtureProcess.mjs's job alone.
  it.each(drivers)('%s starts no child process of its own', (name) => {
    expect(readFileSync(join(dir, name), 'utf8')).not.toMatch(/child_process|\bspawn\w*\(|\bexec\w*\(/)
  })

  it('start the helper with the allowlist that keeps the temporary roots, and activate it only there', () => {
    const source = readFileSync(join(dir, 'proxy-subdomain.test.mjs'), 'utf8')
    expect(source).toMatch(/fixtureEnv\(process\.env, \{ PROXY_BROWSER_FIXTURE: '1' \}\)/)
    expect(readFileSync(join(dir, 'fixtureProcess.mjs'), 'utf8')).not.toMatch(/PROXY_BROWSER_FIXTURE(?!S)/)
  })
})

describe('reading a printed line from a child\'s output', () => {
  it('ignores an unterminated tail and finds the line once its newline arrived', () => {
    const part = 'noise\nPROXY_BROWSER_FIXTURES [{"stop":"x",'
    expect(completeLine(part, 'PROXY_BROWSER_FIXTURES ')).toBeUndefined()
    expect(completeLine(part + '"board":"b"}]', 'PROXY_BROWSER_FIXTURES ')).toBeUndefined()
    expect(completeLine(part + '"board":"b"}]\n', 'PROXY_BROWSER_FIXTURES ')).toBe('PROXY_BROWSER_FIXTURES [{"stop":"x","board":"b"}]')
    expect(completeLine('PROXY_BROWSER_FIXTURES []\r\n', 'PROXY_BROWSER_FIXTURES ')).toBe('PROXY_BROWSER_FIXTURES []')
  })

  it('accepts a fixtures line that arrives in two reads, the second only after the first was read', async () => {
    // The stand-in writes the second half when a byte arrives on its input; the byte is sent once the reader
    // holds the first half, so the reader certainly sees the line in two pieces, with no timing involved.
    const script = `process.stdout.write('PROXY_BROWSER_FIXTURES [{"stop":"x",');process.stdin.once('data',()=>process.stdout.write('"board":"b"}]\\n'))`
    const run = await startFixture({
      command: process.execPath,
      args: ['-e', script],
      onOutput: (output, send) => { if (output.endsWith('"stop":"x",')) send('go') },
    })
    expect(run.fixtures).toEqual([{ stop: 'x', board: 'b' }])
    await run.stop()
  })
})


// ---- ownership of the helper process through complete shutdown ----

const made: string[] = []
afterEach(() => {
  for (const dir of made.splice(0)) rmSync(dir, { recursive: true, force: true })
})
/** A folder of this test's own, the `tmp` a fixture makes its build folder in. */
const own = (): string => {
  const dir = mkdtempSync(join(tmpdir(), 'omo-fixture-test-'))
  made.push(dir)
  return dir
}

/** A Go module with one test that prints its process id as its fixtures line and then waits, as the real helper waits for its stop request. */
function goHelper(body = 'io.Copy(io.Discard, os.Stdin)'): string {
  const dir = own()
  writeFileSync(join(dir, 'go.mod'), 'module helper\n\ngo 1.21\n')
  writeFileSync(
    join(dir, 'helper_test.go'),
    `package helper

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
)

var _ = io.Discard

func TestHelper(t *testing.T) {
	if flag.NArg() > 0 {
		conn, err := net.Dial("tcp", flag.Arg(0))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
	}
	fmt.Printf("PROXY_BROWSER_FIXTURES [{\\"pid\\":\\"%d\\"}]\\n", os.Getpid())
	${body}
}
`,
  )
  return dir
}
const HELPER_ARGS = ['-test.run=^TestHelper$', '-test.count=1']

describe('the helper process is the process that owns the servers', () => {
  it('runs the built test binary directly: the process held is the one that printed its fixtures', async () => {
    const module = goHelper()
    const tmp = own()
    let held = 0
    const run = await startFixture({ cwd: module, build: { pkg: '.' }, args: HELPER_ARGS, tmp, onSpawn: (pid) => { held = pid } })
    // `go test` would be the held child and the test binary its child with another id.
    expect(run.fixtures).toEqual([{ pid: String(held) }])
    expect(readdirSync(tmp)).toHaveLength(1)
    expect(await run.stop()).toBeNull()
    expect(alive(held)).toBe(false)
    expect(readdirSync(tmp)).toEqual([])
  }, 120_000)

  it('waits for the exit and removes the build folder when the helper ends by its own stop address', async () => {
    const module = goHelper('select {}')
    const tmp = own()
    // The helper is a Go test binary that does not serve a stop address, so give it one with a stand-in: node ends by itself once asked.
    const script = `
      const server = require('node:http').createServer((request, response) => response.end('ok', () => process.exit(0)))
      server.listen(0, '127.0.0.1', () => process.stdout.write('PROXY_BROWSER_FIXTURES ' + JSON.stringify([{ stop: 'http://127.0.0.1:' + server.address().port + '/stop' }]) + '\\n'))`
    const run = await startFixture({ cwd: module, build: { pkg: '.' }, command: process.execPath, args: ['-e', script], tmp })
    expect(await run.stop(run.fixtures[0]!.stop)).toBe(0)
    expect(readdirSync(tmp)).toEqual([])
  })

  it('reports a failed build with its output and leaves no folder', async () => {
    const module = goHelper()
    writeFileSync(join(module, 'broken_test.go'), 'package helper\n\nfunc broken( {\n')
    const tmp = own()
    await expect(startFixture({ cwd: module, build: { pkg: '.' }, args: HELPER_ARGS, tmp })).rejects.toThrow(/building the helper failed.*broken_test\.go/s)
    expect(readdirSync(tmp)).toEqual([])
  }, 120_000)

  it('ends the built helper and removes the folder when its fixtures line cannot be read', async () => {
    const module = goHelper()
    writeFileSync(join(module, 'helper_test.go'), readFileSync(join(module, 'helper_test.go'), 'utf8').replace('[{\\"pid', 'not-json [{\\"pid'))
    const tmp = own()
    let held = 0
    await expect(startFixture({ cwd: module, build: { pkg: '.' }, args: HELPER_ARGS, tmp, onSpawn: (pid) => { held = pid } })).rejects.toThrow(/not JSON/)
    expect(held).toBeGreaterThan(0)
    expect(alive(held)).toBe(false)
    expect(readdirSync(tmp)).toEqual([])
  }, 120_000)

  it('leaves no folder behind when the program cannot be started', async () => {
    const tmp = own()
    await expect(startFixture({ command: 'omo-no-such-program-for-the-test', tmp })).rejects.toThrow(/ENOENT/)
    expect(readdirSync(tmp)).toEqual([])
  })
})

describe('the helper must be the process that owns the servers', () => {
  it.each(['go', 'npm', 'npx', 'sh', '/bin/bash', 'C:\\Program Files\\Go\\bin\\go.exe', 'CMD.EXE', 'pwsh'])('refuses the launcher %s as the helper', async (command) => {
    const tmp = own()
    expect(isLauncher(command)).toBe(true)
    await expect(startFixture({ command, args: ['test', './internal/server'], tmp })).rejects.toThrow(/launcher/)
    expect(readdirSync(tmp)).toEqual([])
  })

  it('accepts a program that is not a launcher, including a path with a launcher\'s name inside it', () => {
    for (const command of [process.execPath, '/home/x/go/bin/server.test', 'C:\\gopher\\fixture.exe']) expect(isLauncher(command), command).toBe(false)
  })
})

describe('a kill that fails', () => {
  it('is reported by stop, and the build folder and the interrupt handlers are released all the same', async () => {
    const tmp = own()
    const listeners = process.listenerCount('SIGINT')
    // The helper reports through a connection that closes when its process is gone, so the test can end it afterwards without sleeping.
    const server = createServer()
    const helperGone = new Promise<void>((ok) => server.once('connection', (socket) => socket.once('close', () => ok())))
    await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok))
    let held = 0
    // `end.kill` replaces the whole platform step (group kill, child.kill, taskkill), so this runs the same on every platform.
    const denied = () => { throw Object.assign(new Error('operation not permitted'), { code: 'EPERM' }) }
    try {
      const run = await startFixture({
        cwd: goHelper(),
        build: { pkg: '.' },
        args: [...HELPER_ARGS, '--', `127.0.0.1:${(server.address() as { port: number }).port}`],
        tmp,
        end: { kill: denied },
        onSpawn: (pid) => { held = pid },
      })
      expect(process.listenerCount('SIGINT')).toBe(listeners + 1)
      const failure = await run.stop().then(() => new Error('stop did not reject'), (error: Error) => error)
      expect(failure.message).toMatch(/not permitted/)
      // The process still runs. Where a running executable can be deleted the folder is gone; where it cannot (Windows)
      // the folder stays, and then the failure must say so: a folder is never left behind without being reported.
      expect(readdirSync(tmp).length === 0 || /could not remove the build folder/.test(failure.message), failure.message).toBe(true)
      expect(process.listenerCount('SIGINT')).toBe(listeners)
    } finally {
      // The refused kill left the helper running: end it for real (a directly owned process, ended the same way on every platform).
      if (held > 0) process.kill(held, 'SIGKILL')
      await helperGone
      server.close()
    }
  }, 120_000)

  it('is reported by stop for a helper that has no folder of its own, and the handlers are released', async () => {
    const listeners = process.listenerCount('SIGINT')
    let held = 0
    const denied = () => { throw Object.assign(new Error('operation not permitted'), { code: 'EPERM' }) }
    const run = await startFixture({
      command: process.execPath,
      args: ['-e', `process.stdout.write('PROXY_BROWSER_FIXTURES []\\n'); process.stdin.resume()`],
      end: { kill: denied },
      onSpawn: (pid) => { held = pid },
    })
    try {
      expect(process.listenerCount('SIGINT')).toBe(listeners + 1)
      await expect(run.stop()).rejects.toThrow(/not permitted/)
      expect(process.listenerCount('SIGINT')).toBe(listeners)
      expect(alive(held)).toBe(true)
    } finally {
      process.kill(held, 'SIGKILL')
    }
  })
})

describe('a build folder that cannot be removed', () => {
  it('is reported by stop after the helper was closed, with the handlers released', async () => {
    const tmp = own()
    const listeners = process.listenerCount('SIGINT')
    let held = 0
    let runningWhenRemoved: boolean | undefined
    const remove = () => {
      runningWhenRemoved = alive(held)
      throw Object.assign(new Error('the folder is in use'), { code: 'EBUSY' })
    }
    const run = await startFixture({ cwd: goHelper(), build: { pkg: '.' }, args: HELPER_ARGS, tmp, remove, onSpawn: (pid) => { held = pid } })
    expect(process.listenerCount('SIGINT')).toBe(listeners + 1)
    await expect(run.stop()).rejects.toThrow(/could not remove the build folder.*in use/s)
    // The removal was tried only once the process was closed, and the failure did not keep the handlers.
    expect(runningWhenRemoved).toBe(false)
    expect(process.listenerCount('SIGINT')).toBe(listeners)
    // A failure that persists is reported again, not forgotten.
    await expect(run.stop()).rejects.toThrow(/could not remove the build folder/)
  }, 120_000)

  it('is reported together with a kill that failed', async () => {
    const tmp = own()
    let held = 0
    const denied = () => { throw Object.assign(new Error('operation not permitted'), { code: 'EPERM' }) }
    const stuck = () => { throw Object.assign(new Error('the folder is in use'), { code: 'EBUSY' }) }
    const run = await startFixture({ cwd: goHelper(), build: { pkg: '.' }, args: HELPER_ARGS, tmp, end: { kill: denied }, remove: stuck, onSpawn: (pid) => { held = pid } })
    try {
      const failure = await run.stop().then(() => new Error('stop did not reject'), (error: Error) => error)
      expect(failure.message).toMatch(/not permitted/)
      expect(failure.message).toMatch(/could not remove the build folder.*in use/s)
    } finally {
      process.kill(held, 'SIGKILL')
    }
  }, 120_000)
})

describe('ending a process', () => {
  const running = (extra: object = {}) => ({ pid: 4242, exitCode: null, signalCode: null, kill: () => true, ...extra })

  it('ends a Windows process tree with taskkill when the program starts others itself, and fails loudly when that fails', () => {
    // Injected platform: this checks the command, not a Windows run.
    const asked: number[] = []
    endProcess(running(), { tree: true, platform: 'win32', killTree: (pid: number) => { asked.push(pid) } })
    expect(asked).toEqual([4242])
    expect(taskkillArgs(4242)).toEqual(['/PID', '4242', '/T', '/F'])
    const refuse = () => { throw new Error('Access is denied') }
    expect(() => endProcess(running(), { tree: true, platform: 'win32', killTree: refuse })).toThrow(/Access is denied/)
    // A failure of a process that has ended meanwhile is no failure.
    expect(() => endProcess(running({ exitCode: 1 }), { tree: true, platform: 'win32', killTree: refuse })).not.toThrow()
  })

  it('kills a directly owned Windows program with child.kill and a POSIX one through its process group', () => {
    const killed: string[] = []
    endProcess(running({ kill: (signal: string) => { killed.push(signal); return true } }), { platform: 'win32' })
    expect(killed).toEqual(['SIGKILL'])
    const groups: number[] = []
    endProcess(running(), { platform: 'linux', killGroup: (pid: number) => { groups.push(pid) } })
    expect(groups).toEqual([4242])
  })

  it('hands the kill to an injected end.kill on every platform, and only for a process that is still running', () => {
    for (const platform of ['win32', 'linux', 'darwin'] as const) {
      const asked: Array<[number, boolean]> = []
      const kill = (child: { pid?: number }, options: { tree: boolean }) => { asked.push([child.pid!, options.tree]) }
      const real = () => { throw new Error('the platform step must not run') }
      endProcess(running(), { platform, tree: true, kill, killTree: real, killGroup: real })
      endProcess(running({ kill: real }), { platform, kill, killTree: real, killGroup: real })
      endProcess(running({ exitCode: 0 }), { platform, kill, killTree: real, killGroup: real })
      endProcess(running({ pid: undefined }), { platform, kill, killTree: real, killGroup: real })
      expect(asked, platform).toEqual([[4242, true], [4242, false]])
    }
    const denied = () => { throw new Error('operation not permitted') }
    expect(() => endProcess(running(), { kill: denied })).toThrow(/not permitted/)
  })

  it('refuses a one-platform kill step without an explicit platform, wherever the call is written and whatever the process', () => {
    const step = () => {}
    for (const options of [{ killGroup: step }, { killTree: step }, { killGroup: step, killTree: step, tree: true }]) {
      expect(() => endProcess(running(), options)).toThrow(/platform/)
      expect(() => endProcess(running({ exitCode: 0 }), options)).toThrow(/platform/)
    }
    expect(() => endProcess(running(), { platform: 'linux', killGroup: step })).not.toThrow()
    expect(() => endProcess(running(), { kill: () => {} })).not.toThrow()
  })

  it('refuses it at the start of a fixture too, before anything is built or started', async () => {
    const tmp = own()
    await expect(startFixture({ command: process.execPath, tmp, end: { killGroup: () => {} } })).rejects.toThrow(/platform/)
    expect(readdirSync(tmp)).toEqual([])
  })

  it('does not call a refused kill "already gone": only an empty group is', () => {
    const denied = () => { throw Object.assign(new Error('operation not permitted'), { code: 'EPERM' }) }
    const empty = () => { throw Object.assign(new Error('no such process'), { code: 'ESRCH' }) }
    expect(() => endProcess(running(), { platform: 'linux', killGroup: denied })).toThrow(/not permitted/)
    expect(() => endProcess(running(), { platform: 'linux', killGroup: empty })).not.toThrow()
    expect(() => endProcess(running({ pid: undefined }), { platform: 'linux', killGroup: denied })).not.toThrow()
  })
})

describe('stopping while the process end is held back', () => {
  // The kill is asked for through the seam and takes effect only when the test lets it: the process is certainly still running
  // while the folder is checked, so a stop that removed the folder before the close would fail here on every run, fast or slow machine.
  it('keeps the build folder until the held kill took effect and the process is closed', async () => {
    const tmp = own()
    let release!: () => void
    let helper = 0
    let asked!: () => void
    const kill = new Promise<void>((ok) => { asked = ok })
    const run = await startFixture({
      cwd: goHelper(),
      build: { pkg: '.' },
      args: HELPER_ARGS,
      tmp,
      end: { kill: (child) => { release = () => { child.kill('SIGKILL') }; asked() } },
      onSpawn: (pid) => { helper = pid },
    })
    const stopping = run.stop()
    await kill
    expect(alive(helper)).toBe(true)
    expect(readdirSync(tmp)).toHaveLength(1)
    release()
    expect(await stopping).toBeNull()
    expect(alive(helper)).toBe(false)
    expect(readdirSync(tmp)).toEqual([])
  }, 120_000)
})

describe('a driver that is interrupted', () => {
  // The Go helper connects to this test (its address after `--`); the kernel closes that connection when the helper's
  // process is gone, so the close is the proof that it ended: no sleeping, and no lookup of a process nobody reaped.
  // The driver's kill is held back (`end.kill`) until the test lets it through, as if the platform ended the process
  // late: the folder of a running program must stay until the close has been awaited, and then go.
  //
  // `emit` runs the driver's own interrupt handler by emitting the event inside the driver, which works on every platform;
  // `signal` delivers a real SIGINT to the driver process.
  async function interrupt(how: 'emit' | 'signal'): Promise<string | null> {
    const tmp = own()
    const module = goHelper('io.Copy(io.Discard, os.Stdin)')
    const driver = join(own(), 'driver.mjs')
    writeFileSync(
      driver,
      `import { startFixture } from ${JSON.stringify(new URL('../../scripts/fixtureProcess.mjs', import.meta.url).href)}
await startFixture({
  cwd: process.argv[3], build: { pkg: '.' }, tmp: process.argv[4],
  args: ['-test.run=^TestHelper$', '-test.count=1', '--', '127.0.0.1:' + process.argv[2]],
  end: { kill: (child) => { process.stdout.write('KILL-HELD\\n'); process.stdin.once('data', () => child.kill('SIGKILL')) } },
})
if (process.argv[5] === 'emit') process.stdin.once('data', () => process.emit('SIGINT', 'SIGINT'))
process.stdout.write('READY\\n')
setInterval(() => {}, 1 << 30)
`,
    )
    const server = createServer()
    const helperGone = new Promise<void>((ok) => server.once('connection', (socket) => socket.once('close', () => ok())))
    await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok))
    const port = (server.address() as { port: number }).port
    const child = spawn(process.execPath, [driver, String(port), module, tmp, how], { stdio: ['pipe', 'pipe', 'inherit'] })
    const closed = new Promise<string | null>((ok) => child.once('close', (_code, signal) => ok(signal)))
    const printed = (word: string) => new Promise<void>((ok, bad) => {
      let seen = ''
      child.stdout.on('data', (d: Buffer) => { seen += d; if (seen.includes(word)) ok() })
      child.once('close', () => bad(new Error(`the driver ended before it printed ${word}`)))
    })
    try {
      const ready = printed('READY')
      const held = printed('KILL-HELD')
      await ready
      expect(readdirSync(tmp)).toHaveLength(1)
      if (how === 'emit') child.stdin.write('interrupt')
      else child.kill('SIGINT')
      await held
      // The kill was asked for and has not taken effect: the process is running, so its folder is still there.
      expect(readdirSync(tmp)).toHaveLength(1)
      child.stdin.write('go')
      const signal = await closed
      await helperGone
      expect(readdirSync(tmp)).toEqual([])
      return signal
    } finally {
      server.close()
    }
  }

  it('waits for the helper to be closed, removes the build folder, then ends itself', async () => {
    const signal = await interrupt('emit')
    // Windows reports no signal name for a process that process.kill ended, so the re-raised SIGINT cannot be read back there.
    if (process.platform !== 'win32') expect(signal).toBe('SIGINT')
  }, 120_000)

  // Windows has no catchable interrupt signal for process.kill to deliver (it ends the process at once), so a real signal
  // cannot reach the handler there; the handler's sequencing is covered above on every platform.
  it.skipIf(process.platform === 'win32')('does the same when a real SIGINT is delivered to the driver', async () => {
    expect(await interrupt('signal')).toBe('SIGINT')
  }, 120_000)
})

// ---- conditional skips in tests ----

describe('tests that are skipped', () => {
  const walk = (dir: string): string[] =>
    readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
      if (e.name.startsWith('.') || e.name === 'node_modules' || e.name === 'dist') return []
      const path = join(dir, e.name)
      return e.isDirectory() ? walk(path) : /\.(test|spec)\.(ts|mjs)$/.test(e.name) ? [path] : []
    })
  // Every web test file, guards included: unit tests, browser specs and the standalone drivers.
  const files = walk(resolve('.')).map((path) => ({ path: path.slice(resolve('.').length + 1).split('\\').join('/'), text: readFileSync(path, 'utf8') }))
  // Every way a test is left out, whatever its condition says or hides: a variable, a helper, an import, a platform name.
  const SKIPS = /(?:\.(?:skipIf|runIf|skip|fixme|todo)|\b(?:xit|xtest|xdescribe))\s*\(/g
  const skipsIn = (text: string) => text.match(SKIPS) ?? []

  // The whole inventory. A skip that is not here fails until someone states why; a platform skip needs a capability the platform truly lacks.
  const allowed: Record<string, { count: number; reason: string }> = {
    'src/testing/fixtureProcess.test.ts': {
      count: 1,
      reason: 'platform capability: a real SIGINT cannot be delivered to a process on Windows (process.kill ends it at once); the same handler sequence runs everywhere through an emitted event',
    },
    'e2e/filters.spec.ts': {
      count: 1,
      reason: 'not a platform skip: the screenshot-only test runs only when OMO_SCREENSHOTS names a folder to write to',
    },
  }

  it('are found only in the inventory, in every web test file, whatever their condition', () => {
    const found: Record<string, number> = {}
    for (const { path, text } of files) {
      const n = skipsIn(text).length
      if (n > 0) found[path] = n
    }
    expect(found).toEqual(Object.fromEntries(Object.entries(allowed).map(([path, { count }]) => [path, count])))
    for (const [path, { reason }] of Object.entries(allowed)) expect(reason.length, path).toBeGreaterThan(20)
  })

  it('see a skip whose condition is a variable, a helper or a platform name, in one line or several', () => {
    // Written in pieces so that this file's own text holds no skip for the scan above to count.
    const dot = (name: string) => `it.${name}` + '('
    const forms = [
      "const WINDOWS = process.platform === 'win32'\n" + dot('skipIf') + "WINDOWS)('x', () => {})",
      dot('skipIf') + "isWindows())('x', () => {})",
      dot('skipIf') + "\n  process.platform === 'win32',\n)('x', () => {})",
      dot('runIf') + "IS_LINUX)('x', () => {})",
      'test.skip' + "(WINDOWS, 'x')",
      'test.fix' + "me(WINDOWS, 'x')",
      'describe.skip' + "('x', () => {})",
      'x' + "it('x', () => {})",
    ]
    for (const form of forms) expect(skipsIn(form), form).toHaveLength(1)
    expect(skipsIn('expect(skipped).toBe(1); const skipIfNot = 1; items.skipped(2)')).toHaveLength(0)
    expect(files.some((f) => f.path.startsWith('e2e/') && f.path.endsWith('.spec.ts'))).toBe(true)
    expect(files.some((f) => f.path.startsWith('scripts/') && f.path.endsWith('.test.mjs'))).toBe(true)
    expect(files.some((f) => f.path.endsWith('.guard.test.ts'))).toBe(true)
  })
})
