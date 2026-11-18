// Exercise resource acquisition against real children. A bind collision may be
// retried before the test begins; an assertion or an unrelated error may not.
import { accessSync, constants, copyFileSync, existsSync, mkdirSync, readdirSync, unlinkSync } from 'node:fs'
import { createServer } from 'node:net'
import { dirname, join } from 'node:path'
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
    await expect(handle.omo.start()).rejects.toThrow(/can(?:no|')t assign requested address|not valid in its context/i)
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

test('fake programs exist for the whole life of the test server and go with its home', async ({ repo }) => {
  test.skip(process.platform === 'win32', 'the fake programs are executable files made with POSIX modes')
  const handle = await createOmo({ programs: { files: { claude: '#!/bin/sh\n', agy: '' }, git: true } }, repo)
  try {
    const dir = handle.omo.programs
    expect(dir).toBe(join(handle.omo.home, 'programs'))
    expect(readdirSync(dir).sort()).toEqual(['agy', 'claude', 'git'])
    for (const name of ['agy', 'claude', 'git']) expect(() => accessSync(join(dir, name), constants.X_OK)).not.toThrow()
    await handle.omo.start()
    await handle.omo.stop()
    await handle.omo.start()
    expect(readdirSync(dir).sort(), 'a restart of the server finds the same programs').toEqual(['agy', 'claude', 'git'])
  } finally {
    await handle.dispose()
  }
  expect(existsSync(handle.omo.programs)).toBe(false)
})

test('a test that has no fake programs has no programs folder', async ({ omo }) => {
  expect(omo.programs).toBe('')
})

test('fake programs and an own PATH are alternatives, and a refused creation leaves no home behind', async ({ repo }) => {
  // Homes are made below TMPDIR: point it at a folder of this test (inside the repo fixture's removed folder) and watch it.
  const watched = join(dirname(repo), 'homes')
  mkdirSync(watched)
  const inherited = process.env.TMPDIR
  process.env.TMPDIR = watched
  try {
    await expect(createOmo({ programs: { files: {} }, path: '/bin' }, repo)).rejects.toThrow(/not both/)
    // Fails after the home exists: a program name with a folder in it cannot be written.
    await expect(createOmo({ programs: { files: { 'a/b': '' } } }, repo)).rejects.toThrow()
    expect(readdirSync(watched)).toEqual([])
  } finally {
    if (inherited === undefined) delete process.env.TMPDIR
    else process.env.TMPDIR = inherited
  }
})
