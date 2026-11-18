// @vitest-environment node
import { createServer, type Socket } from 'node:net'
import { describe, expect, it } from 'vitest'
import { run } from '../../e2e/support/run'

describe('running a program of the browser tests to completion', () => {
  it('answers its exit code and output', async () => {
    const result = await run(process.execPath, ['-e', 'process.stdout.write("out"); process.stderr.write("err"); process.exit(3)'], process.env)
    expect(result).toEqual({ code: 3, stdout: 'out', stderr: 'err' })
  })

  it('feeds standard input to the program', async () => {
    const result = await run(process.execPath, ['-e', 'process.stdin.pipe(process.stdout)'], process.env, 'hello')
    expect(result.stdout).toBe('hello')
  })

  // The program never ends by itself and holds a connection to this test, which the kernel closes when its process is
  // gone. The limit is reached (here by the test's own abort, once the program is certainly running) and the rejection
  // must not arrive before that close.
  it('rejects a program that is past its limit only after the program has ended', async () => {
    const server = createServer()
    let gone = false
    const closed = new Promise<void>((ok) => server.once('connection', (socket: Socket) => { socket.once('close', () => { gone = true; ok() }) }))
    const running = new Promise<void>((ok) => server.once('connection', () => ok()))
    await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok))
    const port = (server.address() as { port: number }).port
    const script = `require('node:net').connect(${port}, '127.0.0.1'); setInterval(() => {}, 1 << 30)`
    const limit = new AbortController()
    try {
      const pending = run(process.execPath, ['-e', script], process.env, undefined, 600_000, limit.signal)
      await running
      limit.abort()
      await expect(pending).rejects.toThrow(/did not finish within/)
      expect(gone).toBe(true)
      await closed
    } finally {
      server.close()
    }
  })
})
