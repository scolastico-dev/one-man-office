// @vitest-environment node
import { spawnSync } from 'node:child_process'
import { readdirSync, readFileSync } from 'node:fs'
import { delimiter, join, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { fixtureEnv, startFixture } from '../../scripts/fixtureProcess.mjs'

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
