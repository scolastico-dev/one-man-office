// Shared setup of the Usage page tests: a session on a fake omo whose reads
// and writes the test answers by path, with If-Match recorded.
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach } from 'vitest'
import { defineComponent, h, type Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, type ApiClient, type ApiResult, type ClientOptions, type SendOptions } from '../../../api/client'
import { createTokenStore } from '../../../auth/token'
import { DIALOGS_IN_PLACE } from '../../../components/ui/dialogHost'
import { createSession, SESSION } from '../../../session'
import { provideBoard } from '../../../store'
import { FakeOmo, MemoryStorage } from '../../../testing/fakeOmo'

export interface Sent {
  method: string
  path: string
  body: unknown
  ifMatch: string | undefined
}

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const noResponder = (what: string) => () => {
  throw new ApiError(404, 'not_found', `No route ${what}`)
}

/** A session on a fake omo; `state.get` answers reads by path (else the fake's own), `state.write` every write. */
export async function setup() {
  const omo = new FakeOmo()
  const sent: Sent[] = []
  const state: { get: Record<string, () => unknown>; write: (s: Sent) => unknown } = {
    get: {},
    write: noResponder('for writes'),
  }
  const makeClient = (o: ClientOptions): ApiClient => {
    const base = omo.makeClient(o)
    return {
      ...base,
      get: async <T,>(path: string, query?: Record<string, string | number | undefined>) => {
        const answer = state.get[path]
        if (answer === undefined) return base.get<T>(path, query)
        return { data: JSON.parse(JSON.stringify(answer())) as T, eventId: null }
      },
      send: async <T,>(method: string, path: string, body?: unknown, opts?: SendOptions): Promise<ApiResult<T>> => {
        const s = { method, path, body: JSON.parse(JSON.stringify(body ?? null)) as unknown, ifMatch: opts?.ifMatch }
        sent.push(s)
        return { data: (await state.write(s)) as T, eventId: null }
      },
    }
  }
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient, connect: omo.connect })
  session.start()
  await flushPromises()
  return { omo, session, sent, state }
}

export async function mountIn(s: Awaited<ReturnType<typeof setup>>, component: Component, props: Record<string, unknown>) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })
  await router.push('/')
  const Host = defineComponent({
    setup() {
      provideBoard(s.session.store)
      return () => h(component, props)
    },
  })
  const w = mount(Host, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: s.session, [DIALOGS_IN_PLACE as symbol]: true } } })
  mounted = w
  await flushPromises()
  return w
}
