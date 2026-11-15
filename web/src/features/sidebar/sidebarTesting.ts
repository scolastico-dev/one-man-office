// Shared setup of the sidebar tests: a session on a fake omo that also answers
// the agent and scheduler reads from lists the test changes.
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, type Component } from 'vue'
import { createMemoryHistory, createRouter, type Router } from 'vue-router'
import type { ApiClient, ApiResult, ClientOptions, Query } from '../../api/client'
import type { AgentSession, SchedulerState } from '../../api/types'
import { createTokenStore } from '../../auth/token'
import { createSession, SESSION, type Session } from '../../session'
import { provideBoard } from '../../store'
import { FakeOmo, MemoryStorage } from '../../testing/fakeOmo'

export function agent(over: Partial<AgentSession> = {}): AgentSession {
  return {
    id: 's1',
    role: 'developer',
    ticket: null,
    project: '',
    name: 'Ada',
    model: 'fake/default',
    state: 'running',
    workdir: '/work',
    end_reason: '',
    purpose: 'work',
    review_round: 0,
    started_at: '2026-11-15T10:00:00Z',
    ended_at: null,
    terminal_id: 't1',
    ...over,
  }
}

export interface SidebarHarness {
  omo: FakeOmo
  session: Session
  /** What `GET /api/agents` answers. */
  agents: { list: AgentSession[]; fail: string }
  scheduler: { state: SchedulerState }
  /** The paths read, in order. */
  reads: string[]
}

export async function sidebarHarness(setup: (omo: FakeOmo) => void = () => {}): Promise<SidebarHarness> {
  const omo = new FakeOmo()
  setup(omo)
  const agents = { list: [] as AgentSession[], fail: '' }
  const scheduler = { state: { running: 0, limit: 4, paused: false } as SchedulerState }
  const reads: string[] = []
  const makeClient = (o: ClientOptions): ApiClient => {
    const base = omo.makeClient(o)
    return {
      ...base,
      get: async <T>(path: string, query?: Query): Promise<ApiResult<T>> => {
        if (path === '/api/agents') {
          reads.push(path)
          if (agents.fail) throw new Error(agents.fail)
          return { data: structuredClone(agents.list) as T, eventId: null }
        }
        if (path === '/api/scheduler') {
          reads.push(path)
          return { data: { ...scheduler.state } as T, eventId: null }
        }
        return base.get<T>(path, query)
      },
    }
  }
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient, connect: omo.connect })
  session.start()
  await flushPromises()
  return { omo, session, agents, scheduler, reads }
}

/** Mounts a component with the session, the store and a router at `path`. */
export async function mountSidebar(hn: SidebarHarness, component: Component, props: Record<string, unknown> = {}, path = '/'): Promise<{ wrapper: VueWrapper; router: Router }> {
  const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/:n(\\d+)', name: 'ticket', component: { render: () => null } },
      { path: '/:p(.*)*', component: { render: () => null } },
    ] })
  await router.push(path)
  const Host = defineComponent({
    setup() {
      provideBoard(hn.session.store)
      return () => h(component, props)
    },
  })
  const wrapper = mount(Host, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: hn.session } } })
  await flushPromises()
  return { wrapper, router }
}

/** Delivers an agent event the way the stream does. */
export function emitAgent(omo: FakeOmo, kind: 'agent.created' | 'agent.updated' | 'agent.ended', a: AgentSession): void {
  omo.emit(kind, { agent: a }, a.ticket)
}
