import type { ErrorEnvelope } from './types'

/** A failed request. `code` is the server's error code, `network` when the request never got an answer, or `http` when the answer was not the server's error envelope. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details?: unknown

  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    if (details !== undefined) this.details = details
  }
}

export interface ApiResult<T> {
  data: T
  /** The `Omo-Event-Id` header: the newest event id after the request committed. */
  eventId: number | null
}

export type Query = Record<string, string | number | boolean | undefined>

export interface ApiClient {
  get<T>(path: string, query?: Query): Promise<ApiResult<T>>
  send<T>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, body?: unknown): Promise<ApiResult<T>>
  /** A GET whose answer is plain text. Failures are thrown as for `get`. */
  getText(path: string, query?: Query): Promise<{ text: string; headers: Headers }>
  /** `GET /api/events` as a stream. Non-2xx answers are returned, not thrown; an aborted request rejects with the abort error. */
  openStream(lastEventId: number | null, signal: AbortSignal): Promise<Response>
}

export interface ClientOptions {
  getToken(): string | null
  /**
   * The server refused `token`. Forgets it only if it is still the stored
   * token, and returns another token to try once (for example one another
   * tab stored meanwhile), else null.
   */
  forgetToken(token: string): string | null
  /** The server accepted `token`. */
  tokenAccepted?(token: string): void
  onUnauthorized(reason: 'missing' | 'rejected'): void
  fetch?: typeof fetch
}

function buildUrl(path: string, query?: Query): string {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined) params.set(k, String(v))
  }
  const qs = params.toString()
  return qs ? `${path}?${qs}` : path
}

function networkError(): ApiError {
  return new ApiError(0, 'network', 'Cannot reach omo. Check that it is still running.')
}

function isEnvelope(v: unknown): v is ErrorEnvelope {
  if (typeof v !== 'object' || v === null) return false
  const e = (v as { error?: unknown }).error
  if (typeof e !== 'object' || e === null) return false
  const { code, message } = e as { code?: unknown; message?: unknown }
  return typeof code === 'string' && typeof message === 'string'
}

async function toError(res: Response): Promise<ApiError> {
  let parsed: unknown
  try {
    parsed = JSON.parse(await res.text())
  } catch {
    parsed = undefined
  }
  if (isEnvelope(parsed)) {
    return new ApiError(res.status, parsed.error.code, parsed.error.message, parsed.error.details)
  }
  return new ApiError(res.status, 'http', `The server answered with status ${res.status}.`)
}

function readEventId(res: Response): number | null {
  const raw = res.headers.get('Omo-Event-Id')
  if (raw === null || !/^\d+$/.test(raw.trim())) return null
  return Number(raw)
}

export function createClient(o: ClientOptions): ApiClient {
  const doFetch: typeof fetch = o.fetch ?? ((input, init) => fetch(input, init))

  async function attempt(url: string, init: RequestInit, token: string | null): Promise<Response> {
    const headers = new Headers(init.headers)
    if (token !== null) headers.set('Authorization', `Bearer ${token}`)
    try {
      return await doFetch(url, { ...init, headers })
    } catch (e) {
      if (init.signal?.aborted) throw e
      throw networkError()
    }
  }

  // Sends the request with the stored token. When it is refused and another
  // token is worth a try (one another tab stored meanwhile, or the one a
  // fresh address replaced), that one is tried once. A refused token is then retried once without any, because
  // the browser supplies Basic credentials itself and `auth: none` needs
  // nothing.
  async function authed(url: string, init: RequestInit): Promise<Response> {
    const token = o.getToken()
    const res = await attempt(url, init, token)
    if (res.status !== 401) {
      if (token !== null) o.tokenAccepted?.(token)
      return res
    }
    if (token === null) {
      // A token stored while this request was on its way (the printed
      // address opened meanwhile) is tried before anything is locked.
      if (o.getToken() !== null) return authed(url, init)
      o.onUnauthorized('missing')
      return res
    }
    const other = o.forgetToken(token)
    if (other !== null && other !== token) {
      const again = await attempt(url, init, other)
      if (again.status !== 401) {
        o.tokenAccepted?.(other)
        return again
      }
      o.forgetToken(other)
    }
    const retry = await attempt(url, init, null)
    if (retry.status === 401) o.onUnauthorized('rejected')
    return retry
  }

  async function run<T>(method: string, url: string, body: unknown, hasBody: boolean): Promise<ApiResult<T>> {
    const init: RequestInit = { method, headers: { Accept: 'application/json' } }
    if (method !== 'GET') {
      ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
      if (hasBody) init.body = JSON.stringify(body)
    }
    const res = await authed(url, init)
    if (!res.ok) throw await toError(res)
    const eventId = readEventId(res)
    if (res.status === 204) return { data: undefined as T, eventId }
    const text = await res.text()
    if (text.trim() === '') return { data: undefined as T, eventId }
    try {
      return { data: JSON.parse(text) as T, eventId }
    } catch {
      throw new ApiError(res.status, 'http', 'The server sent an answer that is not JSON.')
    }
  }

  return {
    get: (path, query) => run('GET', buildUrl(path, query), undefined, false),
    send: (method, path, body) => run(method, path, body, body !== undefined),
    async getText(path, query) {
      const res = await authed(buildUrl(path, query), { method: 'GET', headers: { Accept: 'text/plain' } })
      if (!res.ok) throw await toError(res)
      return { text: await res.text(), headers: res.headers }
    },
    openStream(lastEventId, signal) {
      const headers: Record<string, string> = { Accept: 'text/event-stream' }
      if (lastEventId !== null) headers['Last-Event-ID'] = String(lastEventId)
      return authed('/api/events', { method: 'GET', headers, signal })
    },
  }
}
