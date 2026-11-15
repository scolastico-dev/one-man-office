// Shared setup of the create and project dialog tests: a session on a fake omo
// whose writes are answered by the test.
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, type Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, type ApiClient, type ApiResult, type ClientOptions, type Query } from '../../api/client'
import type { Model } from '../../api/types'
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

export const ok = <T,>(data: T): ApiResult<T> => ({ data, eventId: null })
export const refuse = (code: string, message: string, details?: unknown, status = 422): never => {
  throw new ApiError(status, code, message, details)
}

export interface Harness {
  omo: FakeOmo
  session: Session
  sent: Sent[]
  /** Replaces what answers writes and reads of `/api/models`, `/api/roles`, `/api/profiles`. */
  respond: { write: Responder; models: Model[]; roles: unknown; profiles: unknown }
}

export async function harness(setup: (omo: FakeOmo) => void = () => {}): Promise<Harness> {
  const omo = new FakeOmo()
  setup(omo)
  const sent: Sent[] = []
  const respond: Harness['respond'] = {
    write: () => refuse('not_implemented', 'no responder', undefined, 501),
    models: [],
    roles: [],
    profiles: { profiles: [], errors: [] },
  }
  const makeClient = (o: ClientOptions): ApiClient => {
    const base = omo.makeClient(o)
    return {
      ...base,
      get: <T,>(path: string, query?: Query) => {
        if (path === '/api/models') return Promise.resolve(ok(respond.models as T))
        if (path === '/api/roles') return Promise.resolve(ok(respond.roles as T))
        if (path === '/api/profiles') return Promise.resolve(ok(respond.profiles as T))
        return base.get<T>(path, query)
      },
      send: async <T,>(method: string, path: string, body?: unknown) => {
        const s = { method, path, body }
        sent.push(s)
        return (await respond.write(s)) as ApiResult<T>
      },
    }
  }
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient, connect: omo.connect })
  session.start()
  await flushPromises()
  return { omo, session, sent, respond }
}

/** Mounts a component that needs the session, the store and a router. */
export async function mountWith(h0: Harness, component: Component, props: Record<string, unknown>): Promise<VueWrapper> {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })
  await router.push('/')
  const Host = defineComponent({
    setup() {
      provideBoard(h0.session.store)
      return () => h(component, props)
    },
  })
  const wrapper = mount(Host, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: h0.session } } })
  await flushPromises()
  return wrapper
}
