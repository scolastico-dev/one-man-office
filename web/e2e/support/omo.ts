import { spawn, type ChildProcess } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, realpathSync, rmSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { test as base, type TestInfo } from '@playwright/test'
import { initRepo } from './git'

/** The port a developer's own omo uses; tests must never come near it. */
const DEFAULT_PORT = 8090
const READY_TIMEOUT_MS = 20_000
const STOP_TIMEOUT_MS = 10_000

export const WEB_DIR = resolve(import.meta.dirname, '..', '..')
export const REPO_DIR = resolve(WEB_DIR, '..')
export const BINARY = process.env.OMO_E2E_BIN ? resolve(process.env.OMO_E2E_BIN) : join(REPO_DIR, 'bin', process.platform === 'win32' ? 'omo.exe' : 'omo')
export const SEED_BINARY = join(WEB_DIR, 'e2e', '.bin', process.platform === 'win32' ? 'seed.exe' : 'seed')

/** Credentials used when a test starts omo with `auth: 'basic'`. */
export const BASIC = { username: 'e2e', password: 'e2e-secret' } as const

export interface OmoOptions {
  /** Pause the scheduler after start (default true), so no engine work starts. */
  paused?: boolean
  /** `token` (default), `basic` (credentials in `BASIC`) or `none`. */
  auth?: 'token' | 'basic' | 'none'
  /**
   * A scenario for the seeding tool, run before the first start. The string
   * `{{repo}}` is replaced by the path of the test's git repository. A seeded
   * home gets no project of its own: the scenario lists the projects.
   */
  seed?: object
  /** Start without any project (a fresh home). */
  noProject?: boolean
}

export interface CliResult {
  code: number
  stdout: string
  stderr: string
}

export interface Omo {
  /** Temporary directory used as OMO_HOME (and HOME, XDG_*). Removed after the test. */
  readonly home: string
  readonly port: number
  /** `http://127.0.0.1:<port>`, for direct API calls. */
  readonly base: string
  /** The printed address with the login token, on host omo.localhost. */
  accessUrl: string
  token: string
  /** Id of the project "demo" (empty with `noProject` or a seed). */
  readonly project: string
  /** Ticket numbers of the seed scenario by ref. */
  readonly refs: Readonly<Record<string, number>>
  /** An address of the app on host omo.localhost, without token: `omo.url('/12')`. */
  url(path?: string): string
  /** Calls the API with the credentials of the running mode; throws on status 400 or more with the server's message. */
  api<T = unknown>(method: string, path: string, body?: unknown): Promise<T>
  /** Creates a ticket (task by default, in the demo project) and returns its number. */
  createTicket(o?: { title?: string; type?: 'story' | 'task'; story?: number; projects?: string[] }): Promise<number>
  /** Moves a ticket the way another client would (`before`: number of the card it goes above). */
  moveTicket(n: number, column: string, before?: number): Promise<void>
  /** Runs the omo binary with any arguments against this home. */
  cli(args: string[]): Promise<CliResult>
  /** Runs `omo ai <args>` against this server. */
  ai(args: string[]): Promise<CliResult>
  /** Stops the server (and waits until it has exited). */
  stop(): Promise<void>
  /** Starts the server again on the same home and port; refreshes `token` and `accessUrl`. */
  start(): Promise<void>
  /** Runs the seeding tool; only while the server is stopped. */
  seed(scenario: object): Promise<Record<string, number>>
  /** Everything the server printed so far. */
  output(): string
}

/** An environment that cannot reach anything of the developer's omo, git or shell session. */
function cleanEnv(home: string): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {}
  for (const [k, v] of Object.entries(process.env)) {
    if (k.startsWith('OMO_') || k === 'APPDATA' || k === 'USERPROFILE' || k.startsWith('XDG_')) continue
    env[k] = v
  }
  return {
    ...env,
    OMO_HOME: join(home, 'omo'),
    HOME: home,
    USERPROFILE: home,
    APPDATA: join(home, 'appdata'),
    XDG_CONFIG_HOME: join(home, 'xdg-config'),
    XDG_STATE_HOME: join(home, 'xdg-state'),
    XDG_DATA_HOME: join(home, 'xdg-data'),
  }
}

/** A port that is free right now: listen on 0, read it, close. */
async function freePort(): Promise<number> {
  for (;;) {
    const port = await new Promise<number>((res, rej) => {
      const s = createServer()
      s.once('error', rej)
      s.listen(0, '127.0.0.1', () => {
        const p = (s.address() as { port: number }).port
        s.close(() => res(p))
      })
    })
    if (port !== DEFAULT_PORT) return port
  }
}

function run(bin: string, args: string[], env: NodeJS.ProcessEnv, stdin?: string, timeoutMs = 60_000): Promise<CliResult> {
  return new Promise((res, rej) => {
    const child = spawn(bin, args, { env, stdio: ['pipe', 'pipe', 'pipe'] })
    let stdout = ''
    let stderr = ''
    const timer = setTimeout(() => {
      child.kill('SIGKILL')
      rej(new Error(`${args.join(' ')} did not finish within ${timeoutMs} ms\n${stdout}${stderr}`))
    }, timeoutMs)
    child.stdout.on('data', (d: Buffer) => (stdout += d.toString()))
    child.stderr.on('data', (d: Buffer) => (stderr += d.toString()))
    child.once('error', (e) => {
      clearTimeout(timer)
      rej(e)
    })
    child.once('close', (code) => {
      clearTimeout(timer)
      res({ code: code ?? -1, stdout, stderr })
    })
    child.stdin.end(stdin ?? '')
  })
}

export class ServerError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
  }
}

/** Creates a fresh isolated omo. Not started yet. */
async function createOmo(opts: OmoOptions, repo: string): Promise<{ omo: Omo; dispose(): Promise<void> }> {
  if (!existsSync(BINARY)) {
    throw new Error(`the omo binary ${BINARY} does not exist; run "make build" first (or set OMO_E2E_BIN)`)
  }
  const root = realpathSync(tmpdir())
  const home = realpathSync(mkdtempSync(join(root, 'omo-e2e-')))
  if (!home.startsWith(root) || home === root) throw new Error(`the test home ${home} is not below the temporary folder ${root}`)
  mkdirSync(join(home, 'omo'), { recursive: true })
  const env = cleanEnv(home)
  const auth = opts.auth ?? 'token'

  let port = await freePort()
  let child: ChildProcess | undefined
  let log = ''
  let project = ''
  let refs: Record<string, number> = {}
  let first = true

  const assertIsolated = () => {
    if (port === DEFAULT_PORT) throw new Error(`the test server must not use port ${DEFAULT_PORT}`)
    if (env.OMO_HOME === undefined || !env.OMO_HOME.startsWith(home)) throw new Error('OMO_HOME is outside the temporary home')
  }

  const cli = (args: string[]) => run(BINARY, args, env)
  const mustCli = async (args: string[]) => {
    const r = await cli(args)
    if (r.code !== 0) throw new Error(`omo ${args.join(' ')} failed (${r.code}): ${r.stdout}${r.stderr}`)
    return r
  }

  const omo: Omo = {
    home,
    get port() {
      return port
    },
    get base() {
      return `http://127.0.0.1:${port}`
    },
    accessUrl: '',
    token: '',
    get project() {
      return project
    },
    get refs() {
      return refs
    },
    url: (path = '/') => `http://omo.localhost:${port}${path.startsWith('/') ? path : `/${path}`}`,
    async api<T>(method: string, path: string, body?: unknown): Promise<T> {
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      if (auth === 'basic') headers.Authorization = `Basic ${Buffer.from(`${BASIC.username}:${BASIC.password}`).toString('base64')}`
      else if (auth === 'token') headers.Authorization = `Bearer ${omo.token}`
      const res = await fetch(omo.base + path, { method, headers, ...(body === undefined ? {} : { body: JSON.stringify(body) }) })
      const text = await res.text()
      if (res.status >= 400) {
        let code = ''
        let message = text
        try {
          const e = (JSON.parse(text) as { error?: { code?: string; message?: string } }).error
          code = e?.code ?? ''
          message = e?.message ?? text
        } catch {
          // not JSON: keep the text
        }
        throw new ServerError(res.status, code, `${method} ${path}: ${res.status} ${message}`)
      }
      return (text === '' ? undefined : JSON.parse(text)) as T
    },
    async createTicket(o = {}) {
      const type = o.type ?? 'task'
      const projects = o.projects ?? (omo.project === '' ? [] : [omo.project])
      const t = await omo.api<{ number: number }>('POST', '/api/tickets', {
        type,
        title: o.title ?? `Ticket ${Math.random().toString(36).slice(2, 8)}`,
        ...(type === 'task' || projects.length > 0 ? { projects } : {}),
        ...(o.story === undefined ? {} : { story: o.story }),
      })
      return t.number
    },
    async moveTicket(n, column, before) {
      await omo.api('POST', `/api/tickets/${n}/move`, { column, before: before ?? null })
    },
    cli,
    ai: (args) => cli(['ai', ...args]),
    async stop() {
      const c = child
      if (c === undefined || c.exitCode !== null || c.signalCode !== null) {
        child = undefined
        return
      }
      const exited = new Promise<void>((res) => c.once('exit', () => res()))
      c.kill('SIGTERM')
      const timer = setTimeout(() => c.kill('SIGKILL'), STOP_TIMEOUT_MS)
      await exited
      clearTimeout(timer)
      child = undefined
    },
    async start() {
      assertIsolated()
      if (child !== undefined && child.exitCode === null) throw new Error('the test server is already running')
      for (let attempt = 0; ; attempt++) {
        const failure = await spawnServer()
        if (failure === undefined) break
        // Another process may take the probed port before the first start; a restart keeps its port.
        const taken = /address already in use|only one usage/i.test(failure)
        if (!taken || !first || attempt >= 2) throw new Error(`omo did not start:\n${failure}`)
        port = await freePort()
        await mustCli(['config', 'set', 'port', String(port)])
      }
      const wasFirst = first
      first = false
      if (wasFirst) await afterFirstStart()
    },
    async seed(scenario) {
      if (child !== undefined && child.exitCode === null) throw new Error('seed() works only while the server is stopped')
      assertIsolated()
      if (!existsSync(SEED_BINARY)) throw new Error(`the seeding tool ${SEED_BINARY} is missing; the e2e global setup builds it`)
      const json = JSON.stringify(scenario).replaceAll('{{repo}}', repo.replaceAll('\\', '\\\\'))
      const r = await run(SEED_BINARY, [], env, json)
      if (r.code !== 0) throw new Error(`seed failed (${r.code}): ${r.stdout}${r.stderr}`)
      const out = JSON.parse(r.stdout) as { refs: Record<string, number> }
      refs = { ...refs, ...out.refs }
      return out.refs
    },
    output: () => log,
  }

  /** Starts the child and waits for the printed address; returns its output when it exited instead. */
  async function spawnServer(): Promise<string | undefined> {
    log += `\n--- start on port ${port} ---\n`
    const c = spawn(BINARY, [], { env, stdio: ['ignore', 'pipe', 'pipe'] })
    child = c
    let buffer = ''
    const onData = (d: Buffer) => {
      log += d.toString()
      buffer += d.toString()
    }
    c.once('error', (e) => (buffer += `spawn failed: ${e.message}\n`))
    c.stdout?.on('data', onData)
    c.stderr?.on('data', onData)
    const re = new RegExp(`^omo: http://omo\\.localhost:${port}/(?:#([0-9a-f]{64}))?\\s*$`, 'm')
    const deadline = Date.now() + READY_TIMEOUT_MS
    while (Date.now() < deadline) {
      const m = re.exec(buffer)
      if (m) {
        omo.token = m[1] ?? ''
        omo.accessUrl = omo.token === '' ? omo.url('/') : `http://omo.localhost:${port}/#${omo.token}`
        return undefined
      }
      if (c.exitCode !== null || c.signalCode !== null) {
        child = undefined
        return buffer || 'omo exited without output'
      }
      await new Promise((r) => setTimeout(r, 25))
    }
    await omo.stop()
    throw new Error(`omo printed no address within ${READY_TIMEOUT_MS} ms:\n${buffer}`)
  }

  async function afterFirstStart() {
    if (opts.paused ?? true) await omo.api('PUT', '/api/server/paused', { paused: true })
    if (!opts.noProject && opts.seed === undefined) {
      const p = await omo.api<{ id: string }>('POST', '/api/projects', { path: repo, name: 'demo' })
      project = p.id
    }
  }

  const dispose = async () => {
    await omo.stop()
    for (let i = 0; i < 5; i++) {
      try {
        rmSync(home, { recursive: true, force: true })
        return
      } catch {
        await new Promise((r) => setTimeout(r, 100 * (i + 1)))
      }
    }
  }

  // Configure the home, then seed; the test starts the server.
  try {
    await mustCli(['config', 'set', 'port', String(port)])
    if (auth === 'basic') {
      await mustCli(['config', 'set', 'basic_auth', `${BASIC.username}:${BASIC.password}`])
      await mustCli(['config', 'set', 'auth', 'basic'])
    } else if (auth === 'none') {
      await mustCli(['config', 'set', 'auth', 'none'])
    }
    if (opts.seed !== undefined) await omo.seed(opts.seed)
  } catch (e) {
    await dispose()
    throw e
  }
  return { omo, dispose }
}

export interface E2EFixtures {
  omoOptions: OmoOptions
  /** A git repository (branch main, one commit) in the test's temporary folder. */
  repo: string
  /** A fresh omo on its own home and port, started and, unless asked otherwise, paused. */
  omo: Omo
}

export const test = base.extend<E2EFixtures>({
  omoOptions: [{}, { option: true }],
  repo: async ({}, use) => {
    const root = mkdtempSync(join(realpathSync(tmpdir()), 'omo-e2e-repo-'))
    const dir = join(root, 'demo')
    initRepo(dir)
    try {
      await use(dir)
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  },
  omo: async ({ omoOptions, repo }, use, testInfo: TestInfo) => {
    // Stop and delete even when setup, the test or a timeout fails.
    let handle: Awaited<ReturnType<typeof createOmo>> | undefined
    try {
      handle = await createOmo(omoOptions, repo)
      await handle.omo.start()
      await use(handle.omo)
    } finally {
      if (handle !== undefined) {
        if (testInfo.status !== testInfo.expectedStatus) {
          await testInfo.attach('omo-output.txt', { body: handle.omo.output(), contentType: 'text/plain' })
        }
        await handle.dispose()
      }
    }
  },
})

export { expect } from '@playwright/test'
