// Exercise resource acquisition against real children. A bind collision may be
// retried before the test begins; an assertion or an unrelated error may not.
import { copyFileSync, existsSync, unlinkSync } from 'node:fs'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { BINARY, createOmo, expect, test } from './support/omo'

async function occupy(port: number) {
  const server = createServer()
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject)
    server.listen(port, '127.0.0.1', resolve)
  })
  return () => new Promise<void>((resolve, reject) => server.close((e) => e ? reject(e) : resolve()))
}

test('a first-start bind collision reallocates; a restart collision fails without changing the address', async ({ repo }) => {
  const handle = await createOmo({}, repo)
  try {
    const original = handle.omo.port
    const release = await occupy(original)
    try {
      await handle.omo.start()
      expect(handle.omo.port).not.toBe(original)
      expect(await handle.omo.api('GET', '/api/tickets')).toMatchObject({ tickets: [] })
    } finally {
      await release()
    }
    await handle.omo.stop()
    const stable = handle.omo.port
    const releaseRestart = await occupy(stable)
    try {
      await expect(handle.omo.start()).rejects.toThrow(/address already in use|only one usage/i)
      expect(handle.omo.port).toBe(stable)
    } finally {
      await releaseRestart()
    }
    await handle.omo.start()
    await handle.omo.stop()
  } finally {
    await handle.dispose()
  }
  expect(existsSync(handle.omo.home)).toBe(false)
})

test('an unrelated startup failure propagates without port reallocation and cleans its home', async ({ repo }) => {
  const handle = await createOmo({}, repo)
  try {
    const port = handle.omo.port
    expect((await handle.omo.cli(['config', 'set', 'host', '203.0.113.1'])).code).toBe(0)
    await expect(handle.omo.start()).rejects.toThrow(/cannot assign requested address|not valid in its context/i)
    expect(handle.omo.port).toBe(port)
    expect(handle.omo.output().match(/--- start on port/g)).toHaveLength(1)
  } finally {
    await handle.dispose()
  }
  expect(existsSync(handle.omo.home)).toBe(false)
})

test('a server executable that disappears after configuration fails and can be disposed', async ({ repo }) => {
  const binary = join(repo, process.platform === 'win32' ? 'omo.exe' : 'omo')
  copyFileSync(BINARY, binary)
  const handle = await createOmo({}, repo, binary)
  unlinkSync(binary)
  try {
    await expect(handle.omo.start()).rejects.toThrow(/spawn failed:.*ENOENT/)
  } finally {
    await handle.dispose()
  }
  expect(existsSync(handle.omo.home)).toBe(false)
})
