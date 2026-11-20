import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdtempSync, readFileSync, readdirSync, realpathSync, rmSync, statSync } from 'node:fs'
import { createServer } from 'node:https'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { gzipSync } from 'node:zlib'
import { cleanEnv } from './env'
import { processAlive } from './process'
import { makePlugin, type PluginRepo } from './plugin'
import { expect, NODE_SHIM_BINARY, REPO_DIR, TEST_CERT_BINARY, test as base, type Omo } from './omo'
import type { PluginSettings, PluginTask, ProxyForward } from '../../src/api/types'

/** Minimal deterministic tar with executable modes, handled by the production artifact extractor. */
function archive(files: Record<string, string | Buffer>): Buffer {
  const chunks: Buffer[] = []
  for (const [name, value] of Object.entries(files)) {
    const body = Buffer.isBuffer(value) ? value : Buffer.from(value)
    const header = Buffer.alloc(512)
    header.write('fixture/' + name)
    const octal = (offset: number, length: number, n: number) => header.write(n.toString(8).padStart(length - 1, '0') + '\0', offset, length)
    octal(100, 8, 0o755); octal(108, 8, 0); octal(116, 8, 0)
    octal(124, 12, body.length); octal(136, 12, 0)
    header.fill(32, 148, 156); header.write('0', 156); header.write('ustar\0', 257); header.write('00', 263)
    const checksum = header.reduce((sum, byte) => sum + byte, 0)
    header.write(checksum.toString(8).padStart(6, '0') + '\0 ', 148)
    chunks.push(header, body, Buffer.alloc((512 - body.length % 512) % 512))
  }
  return gzipSync(Buffer.concat([...chunks, Buffer.alloc(1024)]))
}

export interface EditorFixture {
  ca: string
  requests: string[]
  activeDownloads: number
  hold(): void
  release(): void
  install(omo: Omo, options?: { badChecksum?: boolean; unsupported?: boolean }): Promise<PluginRepo>
}

export const IDLE_SECONDS = 4

export const test = base.extend<{ editor: EditorFixture }>({
  editor: async ({}, use) => {
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'omo-editor-fixture-')))
    const ca = join(root, 'ca.pem'), key = join(root, 'key.pem')
    // Only this child receives a cleaned environment; no inherited agent/socket identity.
    // A native program of every platform writes the throw-away certificate: no openssl on the machine is needed.
    execFileSync(TEST_CERT_BINARY, [root], { env: cleanEnv(root), stdio: 'pipe' })
    const entry = readFileSync(join(REPO_DIR, 'web/e2e/fixtures/editor.cjs'), 'utf8')
    // Test-only runtime bridge: the editor artifact's bundled Node is a native shim (web/e2e/nodeshim)
    // that runs the Node of this test run, named by the file next to it. No POSIX shell is involved,
    // so the same archive works on Linux, macOS and Windows. Publisher probes use actual bundled Node instead.
    const shim = readFileSync(NODE_SHIM_BINARY)
    const target = process.execPath + '\n'
    const exe = process.platform === 'win32' ? '.exe' : ''
    const raw = archive({ ['lib/node' + exe]: shim, 'lib/node-target': target, ['node' + exe]: shim, 'node-target': target, 'out/node/entry.js': entry, 'out/node/util.js': 'exports.paths={};', 'out/server-main.js': entry })
    let release = () => {}, gate: Promise<void> | undefined
    const requests: string[] = []
    let activeDownloads = 0
    const server = createServer({ cert: readFileSync(ca), key: readFileSync(key) }, async (req, res) => {
      requests.push(req.url ?? '')
      activeDownloads++
      res.once('close', () => activeDownloads--)
      res.setHeader('Content-Length', raw.length)
      res.write(raw.subarray(0, 10))
      if (gate) await Promise.race([gate, new Promise<void>(resolve => res.once('close', resolve))])
      if (!res.destroyed) res.end(raw.subarray(10))
    })
    await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
    const url = `https://127.0.0.1:${(server.address() as { port: number }).port}/editor.tar.gz`
    const fixture: EditorFixture = {
      ca, requests, get activeDownloads() { return activeDownloads },
      hold() { gate = new Promise<void>(resolve => { release = resolve }) },
      release() { release(); gate = undefined },
      async install(omo, options = {}) {
        const folder = join(REPO_DIR, 'plugins/vscode')
        let lua = readFileSync(join(folder, 'editor.lua'), 'utf8')
        // Replace distribution policy only in the test copy. All orchestration remains shipped code.
        const request = 'local job,why=omo.artifact_start{key=artifact_key,url=url,sha256=checksums[editor][platform]'
        // Fail closed on source drift: this fixture must never contact a publisher.
        expect(lua.split(request)).toHaveLength(2)
        lua = lua.replace(request, `local job,why=omo.artifact_start{key=artifact_key,url=${JSON.stringify(url)},sha256=${JSON.stringify(options.badChecksum ? '0'.repeat(64) : createHash('sha256').update(raw).digest('hex'))}`)
        expect(lua).toContain('idle*60')
        lua = lua.replaceAll('idle*60', String(IDLE_SECONDS))
        // The shipped table pins publisher builds only for some platforms. The local stand-in runs on whichever platform the
        // tests run on, so its copy accepts the host; the genuine refusal is the unsupported case, which keeps the shipped guard.
        const guard = 'if not checksums[editor][platform] then'
        expect(lua.split(guard)).toHaveLength(2)
        if (options.unsupported) lua = 'event.host.os="windows";event.host.arch="arm64"\n' + lua
        else lua = lua.replace(guard, 'if false and not checksums[editor][platform] then')
        const backend: Record<string, string> = {}
        for (const file of ['profile.cjs', 'profile-bridge.cjs', 'preference-sync.cjs', 'preference-history.cjs', 'browser-history.cjs']) backend[file] = readFileSync(join(folder, file), 'utf8')
        for (const file of readdirSync(join(folder, 'vendor'), { recursive: true }) as string[]) {
          const full = join(folder, 'vendor', file)
          if (statSync(full).isFile()) backend[join('vendor', file)] = readFileSync(full, 'utf8')
        }
        const repo = makePlugin(join(omo.home, 'editor-source'), JSON.parse(readFileSync(join(folder, 'plugin.json'), 'utf8')) as object, {
          ...backend,
          'editor.lua': lua,
          'launch.cjs': readFileSync(join(folder, 'launch.cjs'), 'utf8'),
          'frontend.js': readFileSync(join(folder, 'frontend.js'), 'utf8'),
        })
        await omo.api('POST', '/api/plugins', { name: 'vscode', source: repo.source, acknowledge: 'community' })
        return repo
      },
    }
    try { await use(fixture) }
    finally {
      fixture.release()
      server.closeAllConnections()
      await new Promise<void>(resolve => server.close(() => resolve()))
      rmSync(root, { recursive: true, force: true })
    }
  },
  omoOptions: async ({ editor }, use) => { await use({ tlsCA: editor.ca }) },
})

export async function configure(omo: Omo, name: string, patch: Partial<PluginSettings>): Promise<void> {
  const list = await omo.api<{ settings: PluginSettings[] }>('GET', '/api/plugins')
  const current = list.settings.find(s => s.name === name)!
  const { revision: _, ...settings } = current
  await omo.api('PUT', `/api/plugins/${name}`, { ...settings, ...patch })
}
export async function tasks(omo: Omo, name = 'vscode'): Promise<PluginTask[]> {
  return (await omo.api<{ tasks: PluginTask[] }>('GET', `/api/plugins/${name}/tasks`)).tasks
}
export async function forwards(omo: Omo): Promise<ProxyForward[]> {
  return (await omo.api<{ forwards: ProxyForward[] }>('GET', '/api/proxy/forwards')).forwards
}
export async function expectClean(omo: Omo, name = 'vscode'): Promise<void> {
  await expect.poll(async () => (await tasks(omo, name)).filter(t => t.state === 'running')).toEqual([])
  await expect.poll(() => forwards(omo)).toEqual([])
}

/** Only inspect PIDs explicitly emitted by the fixture, never arbitrary process environments. */
export function processTree(task: PluginTask): number[] {
  return task.output.filter(line => /^FIXTURE_(PID|CHILD):\d+$/.test(line)).map(line => Number(line.split(':')[1]))
}
export async function expectTreeGone(pids: number[]): Promise<void> {
  expect(pids).toHaveLength(2)
  await expect.poll(() => pids.filter(processAlive), { message: 'editor and descendant still running' }).toEqual([])
}
