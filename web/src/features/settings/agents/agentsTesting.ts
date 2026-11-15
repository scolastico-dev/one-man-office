// Shared setup of the agents page tests: a session on a fake omo whose
// profile reads and writes the test answers, with If-Match recorded.
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, vi } from 'vitest'
import { defineComponent, h, type Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError, type ApiClient, type ApiResult, type ClientOptions, type Query, type SendOptions } from '../../../api/client'
import type { ProfilesResponse } from '../../../api/types'
import { DIALOGS_IN_PLACE } from '../../../components/ui/dialogHost'
import { createTokenStore } from '../../../auth/token'
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
  vi.unstubAllGlobals()
})

/** A session on a fake omo whose profiles and writes the test answers; If-Match is recorded. */
export async function setup() {
  const omo = new FakeOmo()
  const sent: Sent[] = []
  const state: { profiles: ProfilesResponse; write: (s: Sent) => unknown } = {
    profiles: { profiles: [], errors: [] },
    write: () => {
      throw new ApiError(501, 'not_implemented', 'no responder')
    },
  }
  const makeClient = (o: ClientOptions): ApiClient => {
    const base = omo.makeClient(o)
    return {
      ...base,
      get: <T,>(path: string, query?: Query) =>
        path === '/api/profiles' ? Promise.resolve({ data: JSON.parse(JSON.stringify(state.profiles)) as T, eventId: null }) : base.get<T>(path, query),
      send: async <T,>(method: string, path: string, body?: unknown, opts?: SendOptions): Promise<ApiResult<T>> => {
        const s = { method, path, body: JSON.parse(JSON.stringify(body ?? null)) as unknown, ifMatch: opts?.ifMatch }
        sent.push(s)
        return { data: state.write(s) as T, eventId: null }
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

