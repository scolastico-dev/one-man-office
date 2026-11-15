import { afterEach, describe, expect, it, vi } from 'vitest'
import { createTokenStore } from '../auth/token'
import { ApiError, createClient } from './client'

const TOKEN = 'abababababababababababababababababababababababababababababababab'

interface Call {
  url: string
  method: string
  headers: Headers
  body: string | undefined
}

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } })
}

function setup(answers: Array<Response | Error>, token: string | null = null) {
  const calls: Call[] = []
  const queue = [...answers]
  const fetchFn = (async (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({
      url: String(input),
      method: init?.method ?? 'GET',
      headers: new Headers(init?.headers),
      body: init?.body as string | undefined,
    })
    const next = queue.shift()
    if (next === undefined) throw new Error('unexpected request')
    if (next instanceof Error) throw next
    return next
  }) as typeof fetch
  const state = { token, forgotten: 0, unauthorized: [] as string[] }
  const client = createClient({
    getToken: () => state.token,
    forgetToken: (t: string) => {
      state.forgotten++
      if (state.token === t) state.token = null
      return null
    },
    onUnauthorized: (r) => state.unauthorized.push(r),
    fetch: fetchFn,
  })
  return { client, calls, state }
}

afterEach(() => vi.restoreAllMocks())

describe('authorization', () => {
  it('sends Bearer only when a token is stored', async () => {
    const withToken = setup([json(200, {})], TOKEN)
    await withToken.client.get('/api/server')
    expect(withToken.calls[0]?.headers.get('Authorization')).toBe('Bearer ' + TOKEN)

    const without = setup([json(200, {})])
    await without.client.get('/api/server')
    expect(without.calls[0]?.headers.has('Authorization')).toBe(false)
  })

  it('401 with a stored token retries once without Authorization and forgets the token on success', async () => {
    const t = setup([json(401, { error: { code: 'unauthorized', message: 'no' } }), json(200, { ok: 1 })], TOKEN)
    const r = await t.client.get<{ ok: number }>('/api/server')
    expect(r.data).toEqual({ ok: 1 })
    expect(t.calls).toHaveLength(2)
    expect(t.calls[0]?.headers.get('Authorization')).toBe('Bearer ' + TOKEN)
    expect(t.calls[1]?.headers.has('Authorization')).toBe(false)
    expect(t.state.forgotten).toBe(1)
    expect(t.state.unauthorized).toEqual([])
  })

  it('retries a refused POST with the same body', async () => {
    const t = setup([json(401, { error: { code: 'unauthorized', message: 'no' } }), json(200, {})], TOKEN)
    await t.client.send('POST', '/api/tickets/1/approve', {})
    expect(t.calls[1]?.method).toBe('POST')
    expect(t.calls[1]?.body).toBe('{}')
  })

  it('401 twice reports rejected; 401 without a token reports missing', async () => {
    const e = { error: { code: 'unauthorized', message: 'no' } }
    const twice = setup([json(401, e), json(401, e)], TOKEN)
    await expect(twice.client.get('/api/server')).rejects.toMatchObject({ status: 401, code: 'unauthorized' })
    expect(twice.state.unauthorized).toEqual(['rejected'])
    expect(twice.state.forgotten).toBe(1)

    const none = setup([json(401, e)])
    await expect(none.client.get('/api/server')).rejects.toBeInstanceOf(ApiError)
    expect(none.state.unauthorized).toEqual(['missing'])
    expect(none.calls).toHaveLength(1)
  })
})

describe('a token stored while a request without one is on its way', () => {
  it('tries the request again with that token instead of reporting a missing token', async () => {
    const e = { error: { code: 'unauthorized', message: 'no' } }
    const state = { token: null as string | null, unauthorized: [] as string[] }
    const sent: (string | null)[] = []
    const client = createClient({
      getToken: () => state.token,
      forgetToken: () => null,
      onUnauthorized: (r) => state.unauthorized.push(r),
      fetch: (async (_input: RequestInfo | URL, init?: RequestInit) => {
        const auth = new Headers(init?.headers).get('Authorization')
        sent.push(auth)
        if (auth === null) {
          // The printed address is opened while the answer is on its way.
          state.token = TOKEN
          return json(401, e)
        }
        return json(200, { ok: 1 })
      }) as typeof fetch,
    })
    const r = await client.get<{ ok: number }>('/api/server')
    expect(r.data).toEqual({ ok: 1 })
    expect(sent).toEqual([null, 'Bearer ' + TOKEN])
    expect(state.unauthorized).toEqual([])
  })
})

describe('a token refused in one tab while another tab stores a new one', () => {
  const NEW = 'cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd'
  const refused = () => json(401, { error: { code: 'unauthorized', message: 'no' } })

  function sharedStorage(): Storage {
    const m = new Map<string, string>()
    return {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
      removeItem: (k: string) => void m.delete(k),
    } as unknown as Storage
  }

  it("keeps the other tab's token and retries once with it", async () => {
    const storage = sharedStorage()
    const tabA = createTokenStore(() => storage)
    const tabB = createTokenStore(() => storage)
    tabA.save(TOKEN)
    const auth: string[] = []
    const answers = [refused(), json(200, { ok: 1 })]
    const unauthorized: string[] = []
    const client = createClient({
      getToken: () => tabA.read(),
      forgetToken: (t) => tabA.refused(t),
      onUnauthorized: (r) => unauthorized.push(r),
      fetch: (async (_input: RequestInfo | URL, init?: RequestInit) => {
        auth.push(new Headers(init?.headers).get('Authorization') ?? '')
        // Tab B opens a fresh address while tab A's request with the old token is under way.
        if (auth.length === 1) tabB.capture({ hash: '#' + NEW, pathname: '/', search: '' } as Location, { state: null, replaceState() {} } as unknown as History)
        return answers.shift()!
      }) as typeof fetch,
    })
    const r = await client.get<{ ok: number }>('/api/server')
    expect(r.data).toEqual({ ok: 1 })
    expect(auth).toEqual(['Bearer ' + TOKEN, 'Bearer ' + NEW])
    expect(tabB.read()).toBe(NEW)
    expect(tabA.read()).toBe(NEW)
    expect(unauthorized).toEqual([])
  })

  it('forgets the refused token when it is still the stored one', async () => {
    const storage = sharedStorage()
    const tab = createTokenStore(() => storage)
    tab.save(TOKEN)
    const answers = [refused(), refused()]
    const unauthorized: string[] = []
    const client = createClient({
      getToken: () => tab.read(),
      forgetToken: (t) => tab.refused(t),
      onUnauthorized: (r) => unauthorized.push(r),
      fetch: (async () => answers.shift()!) as typeof fetch,
    })
    await expect(client.get('/api/server')).rejects.toMatchObject({ status: 401 })
    expect(tab.read()).toBeNull()
    expect(storage.getItem('omo.token')).toBeNull()
    expect(unauthorized).toEqual(['rejected'])
  })

  it('a new token that is refused too is forgotten, then the request goes out without a token', async () => {
    const storage = sharedStorage()
    const tab = createTokenStore(() => storage)
    tab.save(TOKEN)
    const auth: string[] = []
    const answers = [refused(), refused(), refused()]
    const client = createClient({
      getToken: () => tab.read(),
      forgetToken: (t) => tab.refused(t),
      onUnauthorized: () => {},
      fetch: (async (_input: RequestInfo | URL, init?: RequestInit) => {
        auth.push(new Headers(init?.headers).get('Authorization') ?? '')
        if (auth.length === 1) storage.setItem('omo.token', NEW)
        return answers.shift()!
      }) as typeof fetch,
    })
    await expect(client.get('/api/server')).rejects.toMatchObject({ status: 401 })
    expect(auth).toEqual(['Bearer ' + TOKEN, 'Bearer ' + NEW, ''])
    expect(tab.read()).toBeNull()
  })
})

describe('a token from the address that replaced a stored one', () => {
  const OLD = 'efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef'
  const refused = () => json(401, { error: { code: 'unauthorized', message: 'no' } })

  function tab(answers: Response[]) {
    const m = new Map<string, string>([['omo.token', OLD]])
    const storage = {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
      removeItem: (k: string) => void m.delete(k),
    } as unknown as Storage
    const tokens = createTokenStore(() => storage)
    tokens.capture({ hash: '#' + TOKEN, pathname: '/', search: '' } as Location, { state: null, replaceState() {} } as unknown as History)
    const auth: string[] = []
    const unauthorized: string[] = []
    const client = createClient({
      getToken: () => tokens.read(),
      forgetToken: (t) => tokens.refused(t),
      tokenAccepted: (t) => tokens.accepted(t),
      onUnauthorized: (r) => unauthorized.push(r),
      fetch: (async (_input: RequestInfo | URL, init?: RequestInit) => {
        auth.push(new Headers(init?.headers).get('Authorization') ?? '')
        const next = answers.shift()
        if (!next) throw new Error('unexpected request')
        return next
      }) as typeof fetch,
    })
    return { client, tokens, storage, auth, unauthorized }
  }

  it('a stale address: the refused new token gives way to the previous one, which is retried once', async () => {
    const t = tab([refused(), json(200, { ok: 1 })])
    expect(t.tokens.read()).toBe(TOKEN)
    const r = await t.client.get<{ ok: number }>('/api/server')
    expect(r.data).toEqual({ ok: 1 })
    expect(t.auth).toEqual(['Bearer ' + TOKEN, 'Bearer ' + OLD])
    expect(t.storage.getItem('omo.token')).toBe(OLD)
    expect(t.unauthorized).toEqual([])
  })

  it('a stale address where the previous token is refused too reports the login as rejected', async () => {
    const t = tab([refused(), refused(), refused()])
    await expect(t.client.get('/api/server')).rejects.toMatchObject({ status: 401 })
    expect(t.auth).toEqual(['Bearer ' + TOKEN, 'Bearer ' + OLD, ''])
    expect(t.tokens.read()).toBeNull()
    expect(t.unauthorized).toEqual(['rejected'])
  })

  it('a rotated token: the new one is accepted and stays, the old one is not brought back later', async () => {
    const t = tab([json(200, {}), refused(), refused()])
    await t.client.get('/api/server')
    expect(t.storage.getItem('omo.token')).toBe(TOKEN)
    // Refused later on: the old token was given up when the new one worked.
    await expect(t.client.get('/api/server')).rejects.toMatchObject({ status: 401 })
    expect(t.auth).toEqual(['Bearer ' + TOKEN, 'Bearer ' + TOKEN, ''])
    expect(t.tokens.read()).toBeNull()
    expect(t.unauthorized).toEqual(['rejected'])
  })
})

describe('requests', () => {
  it('every non-GET has Content-Type application/json, also without a body', async () => {
    const t = setup([json(200, {}), json(200, {}), json(200, {}), json(200, {}), json(200, {})])
    await t.client.send('POST', '/a', { x: 1 })
    await t.client.send('POST', '/b')
    await t.client.send('DELETE', '/c')
    await t.client.send('PUT', '/d', {})
    await t.client.send('PATCH', '/e', {})
    for (const c of t.calls) expect(c.headers.get('Content-Type')).toBe('application/json')
    expect(t.calls[1]?.body).toBeUndefined()
    expect(t.calls[0]?.body).toBe('{"x":1}')
  })

  it('a GET carries no Content-Type and builds the query without undefined values', async () => {
    const t = setup([json(200, {})])
    await t.client.get('/api/tickets', { q: 'a b', story: 3, needs_me: true, project: undefined })
    expect(t.calls[0]?.headers.has('Content-Type')).toBe(false)
    expect(t.calls[0]?.url).toBe('/api/tickets?q=a+b&story=3&needs_me=true')
  })

  it('Omo-Event-Id is returned as eventId', async () => {
    const t = setup([json(200, {}, { 'Omo-Event-Id': '42' }), json(200, {}), json(200, {}, { 'Omo-Event-Id': 'x' })])
    expect((await t.client.send('POST', '/a', {})).eventId).toBe(42)
    expect((await t.client.send('POST', '/a', {})).eventId).toBeNull()
    expect((await t.client.send('POST', '/a', {})).eventId).toBeNull()
  })

  it('a 204 gives undefined data', async () => {
    const t = setup([new Response(null, { status: 204 })])
    expect((await t.client.send('DELETE', '/a')).data).toBeUndefined()
  })
})

describe('errors', () => {
  it('error envelope becomes ApiError with status, code and message', async () => {
    const t = setup([json(409, { error: { code: 'invalid_transition', message: 'Cannot move #3 now.', details: { n: 3 } } })])
    const err = await t.client.send('POST', '/a', {}).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, code: 'invalid_transition', message: 'Cannot move #3 now.', details: { n: 3 } })
  })

  it('a non-JSON 502 becomes ApiError code "http"; a fetch failure code "network"', async () => {
    const t = setup([new Response('<html>bad gateway</html>', { status: 502 }), new TypeError('Failed to fetch')])
    await expect(t.client.get('/a')).rejects.toMatchObject({ status: 502, code: 'http' })
    await expect(t.client.get('/a')).rejects.toMatchObject({ code: 'network' })
  })

  it('a 200 that is not JSON becomes code "http"', async () => {
    const t = setup([new Response('<html></html>', { status: 200 })])
    await expect(t.client.get('/a')).rejects.toMatchObject({ code: 'http' })
  })
})

describe('the token never leaks', () => {
  it('stays out of request URLs, errors, logs and the document title', async () => {
    document.title = 'omo'
    const spies = (['log', 'info', 'warn', 'error', 'debug'] as const).map((k) => vi.spyOn(console, k))
    const e = { error: { code: 'unauthorized', message: 'Locked.' } }
    const t = setup(
      [json(200, {}), json(500, 'oops'), new TypeError('Failed to fetch'), json(401, e), json(401, e), json(200, {})],
      TOKEN,
    )
    const errors: unknown[] = []
    await t.client.get('/api/tickets', { q: 'x' })
    t.state.token = TOKEN
    await t.client.send('POST', '/api/x', {}).catch((x: unknown) => errors.push(x))
    t.state.token = TOKEN
    await t.client.get('/api/y').catch((x: unknown) => errors.push(x))
    t.state.token = TOKEN
    await t.client.get('/api/z').catch((x: unknown) => errors.push(x))
    t.state.token = TOKEN
    await t.client.openStream(7, new AbortController().signal)

    for (const c of t.calls) {
      expect(c.url).not.toContain(TOKEN)
      expect(c.url).not.toContain('#')
    }
    for (const x of errors) {
      const err = x as ApiError
      expect(err.message).not.toContain(TOKEN)
      expect(err.stack ?? '').not.toContain(TOKEN)
      expect(JSON.stringify(err)).not.toContain(TOKEN)
      expect(String(err.cause ?? '')).not.toContain(TOKEN)
    }
    expect(document.title).not.toContain(TOKEN)
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })
})

describe('openStream', () => {
  it('asks for an event stream, resumes with Last-Event-ID and returns the response unread', async () => {
    const t = setup([new Response('', { status: 200 })], TOKEN)
    const res = await t.client.openStream(12, new AbortController().signal)
    expect(res.status).toBe(200)
    expect(t.calls[0]?.url).toBe('/api/events')
    expect(t.calls[0]?.headers.get('Accept')).toBe('text/event-stream')
    expect(t.calls[0]?.headers.get('Last-Event-ID')).toBe('12')
    expect(t.calls[0]?.headers.get('Authorization')).toBe('Bearer ' + TOKEN)
  })

  it('sends no Last-Event-ID for a first connection and returns non-2xx answers', async () => {
    const t = setup([json(400, { error: { code: 'bad_request', message: 'bad' } })])
    const res = await t.client.openStream(null, new AbortController().signal)
    expect(res.status).toBe(400)
    expect(t.calls[0]?.headers.has('Last-Event-ID')).toBe(false)
  })

  it('reports an unauthorized stream and passes an abort through unchanged', async () => {
    const t = setup([json(401, { error: { code: 'unauthorized', message: 'no' } })])
    await t.client.openStream(null, new AbortController().signal)
    expect(t.state.unauthorized).toEqual(['missing'])

    const ac = new AbortController()
    ac.abort()
    const abort = new DOMException('aborted', 'AbortError')
    const u = setup([abort])
    await expect(u.client.openStream(null, ac.signal)).rejects.toBe(abort)
  })
})

describe('getText', () => {
  it('returns the text and the headers, with the token and the query', async () => {
    const t = setup([new Response('hello\nworld', { status: 200, headers: { 'Omo-Log-Truncated': 'true' } })], TOKEN)
    const r = await t.client.getText('/api/sessions/s1/log', { x: 1 })
    expect(r.text).toBe('hello\nworld')
    expect(r.headers.get('Omo-Log-Truncated')).toBe('true')
    expect(t.calls[0]?.url).toBe('/api/sessions/s1/log?x=1')
    expect(t.calls[0]?.headers.get('Authorization')).toBe('Bearer ' + TOKEN)
  })

  it('throws the server error and handles a refused token like get', async () => {
    const gone = setup([json(404, { error: { code: 'not_found', message: 'The log of this session is no longer kept.' } })])
    await expect(gone.client.getText('/api/sessions/s1/log')).rejects.toMatchObject({
      status: 404,
      message: 'The log of this session is no longer kept.',
    })
    const e = { error: { code: 'unauthorized', message: 'no' } }
    const refused = setup([json(401, e), new Response('ok', { status: 200 })], TOKEN)
    expect((await refused.client.getText('/api/sessions/s1/log')).text).toBe('ok')
    expect(refused.calls[1]?.headers.has('Authorization')).toBe(false)
    expect(refused.state.forgotten).toBe(1)
    const down = setup([new Error('offline')])
    await expect(down.client.getText('/x')).rejects.toBeInstanceOf(ApiError)
  })
})
