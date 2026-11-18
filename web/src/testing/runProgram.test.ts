// @vitest-environment node
import { createServer, type Socket } from 'node:net'
import { describe, expect, it } from 'vitest'
import { run } from '../../e2e/support/run'

function alive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

describe('running a program of the browser tests to completion', () => {
  it('answers its exit code and output', async () => {
    const result = await run(process.execPath, ['-e', 'process.stdout.write("out"); process.stderr.write("err"); process.exit(3)'], process.env)
    expect(result).toEqual({ code: 3, stdout: 'out', stderr: 'err' })
  })

  it('feeds standard input to the program', async () => {
    const result = await run(process.execPath, ['-e', 'process.stdin.pipe(process.stdout)'], process.env, 'hello')
    expect(result.stdout).toBe('hello')
  })

  // The program never ends by itself, prints its process id and holds a connection to this test. Two separate facts are
  // checked, each from its own source: when the rejection arrives the program's own process is already gone (the run awaited
  // its own close; the id is in the rejection's output), and the kernel's close of the connection, a callback independent of
  // the run, is awaited before it is read, never assumed to have been dispatched already.
  it('rejects a program that is past its limit only after the program has ended', async () => {
    const server = createServer()
    const closed = new Promise<void>((ok) => server.once('connection', (socket: Socket) => { socket.once('close', () => ok()) }))
    const running = new Promise<void>((ok) => server.once('connection', () => ok()))
    await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok))
    const port = (server.address() as { port: number }).port
    const script = `process.stdout.write('PID ' + process.pid + '\\n'); require('node:net').connect(${port}, '127.0.0.1'); setInterval(() => {}, 1 << 30)`
    const limit = new AbortController()
    try {
      const pending = run(process.execPath, ['-e', script], process.env, undefined, 600_000, limit.signal)
      await running
      limit.abort()
      const failure = await pending.then(() => new Error('the run did not reject'), (error: Error) => error)
      expect(failure.message).toMatch(/did not finish within/)
      const pid = Number(/PID (\d+)/.exec(failure.message)?.[1])
      expect(pid).toBeGreaterThan(0)
      expect(alive(pid)).toBe(false)
      await closed
    } finally {
      server.close()
    }
  })
})
