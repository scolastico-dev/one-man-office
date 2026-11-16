// Shared setup of the ticket panel tests: a session on a fake omo that also
// answers `GET /api/tickets/<n>` with a description, and whose writes are
// answered by the test.
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, type Component } from 'vue'
import { createMemoryHistory, createRouter, type RouteRecordRaw, type Router } from 'vue-router'
import { ApiError, type ApiClient, type ApiResult, type ClientOptions, type Query } from '../../api/client'
import type { Ticket, TicketSummary } from '../../api/types'
import { createTokenStore } from '../../auth/token'
import { createSession, SESSION, type Session } from '../../session'
import { provideBoard } from '../../store'
import { FakeOmo, MemoryStorage } from '../../testing/fakeOmo'

export interface Sent {
  method: string
  path: string
  body: unknown
}

export type Responder = (sent: Sent) => Promise<ApiResult<unknown>> | ApiResult<unknown>

export const ok = <T>(data: T, eventId: number | null = null): ApiResult<T> => ({ data, eventId })
export const refuse = (code: string, message: string, status = 422, details?: unknown): never => {
  throw new ApiError(status, code, message, details)
}

export interface PanelHarness {
  omo: FakeOmo
  session: Session
  sent: Sent[]
  /** Descriptions by ticket number. */
  descriptions: Map<number, string>
  /** Tickets only the server knows (not in the board's snapshot). */
  hidden: TicketSummary[]
  /** Answers writes; refuses everything until a test sets it. */
  write: { fn: Responder }
  /** Answers `GET /api/tickets/<n>`; the default reads the fake's tickets. */
  readTicket: { fn: (n: number) => Promise<ApiResult<Ticket>> }
}

export async function panelHarness(setup: (omo: FakeOmo) => void = () => {}): Promise<PanelHarness> {
  const omo = new FakeOmo()
  setup(omo)
  const sent: Sent[] = []
  const descriptions = new Map<number, string>()
  const hidden: TicketSummary[] = []
  const write = { fn: (() => refuse('not_implemented', 'no responder', 501)) as Responder }
  const readTicket = {
    fn: async (n: number): Promise<ApiResult<Ticket>> => {
      const t = omo.tickets.find((x) => x.number === n) ?? hidden.find((x) => x.number === n)
      if (!t) return refuse('not_found', `no ticket #${n}`, 404)
      return ok({ ...t, description: descriptions.get(n) ?? '' })
    },
  }
  const makeClient = (o: ClientOptions): ApiClient => {
    const base = omo.makeClient(o)
    return {
      ...base,
      get: <T>(path: string, query?: Query) => {
        const m = /^\/api\/tickets\/(\d+)$/.exec(path)
        if (m) return readTicket.fn(Number(m[1])) as Promise<ApiResult<T>>
        return base.get<T>(path, query)
      },
      send: async <T>(method: string, path: string, body?: unknown) => {
        const s = { method, path, body }
        sent.push(s)
        return (await write.fn(s)) as ApiResult<T>
      },
    }
  }
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient, connect: omo.connect })
  session.start()
  await flushPromises()
  return { omo, session, sent, descriptions, hidden, write, readTicket }
}

/** Mounts a component that needs the session, the store and a router, at `path`. */
export async function mountPanel(
  hn: PanelHarness,
  component: Component,
  props: Record<string, unknown>,
  path = '/',
  provide: Record<symbol, unknown> = {},
  o: { slots?: Record<string, () => unknown>; routes?: RouteRecordRaw[] } = {},
): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({ history: createMemoryHistory(), routes: o.routes ?? [{ path: '/:p(.*)*', component: { render: () => null } }] })
  await router.push(path)
  const Host = defineComponent({
    setup() {
      provideBoard(hn.session.store)
      return () => h(component, props, o.slots)
    },
  })
  const wrapper = mount(Host, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: hn.session, ...provide } } })
  await flushPromises()
  return { wrapper, router }
}
