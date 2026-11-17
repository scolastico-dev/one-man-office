// Shared setup of the usage tests: a sidebar session whose client also
// answers the usage read and the refresh from state the test changes.
import type { ApiResult, Query } from '../../api/client'
import type { UsageReading, UsageState } from '../../api/types'
import { sidebarHarness, type SidebarHarness } from './sidebarTesting'

export function reading(over: Partial<UsageReading> = {}): UsageReading {
  return {
    key: '3f9a1c7e2b604d18',
    script: 'claude',
    profiles: ['claude'],
    windows: [{ name: 'session', label: '', used_percent: 42.5, resets_at: null }],
    fetched_at: new Date(Date.now() - 2 * 60_000).toISOString(),
    next_at: null,
    running: false,
    error: '',
    stale_since: null,
    ...over,
  }
}

export interface UsageHarness extends SidebarHarness {
  /** What `GET /api/usage` answers. */
  usage: { state: UsageState; fail: string; reads: number }
  /** What `POST /api/usage/refresh` answers; `refuse` makes it fail. */
  refresh: { state: UsageState; refuse: string; calls: number; gate: Promise<void> | null }
}

export async function usageHarness(first: UsageState = { readings: [], models: [] }): Promise<UsageHarness> {
  const hn = await sidebarHarness()
  const usage = { state: first, fail: '', reads: 0 }
  const refresh = { state: first, refuse: '', calls: 0, gate: null as Promise<void> | null }
  const client = hn.session.client
  const get = client.get.bind(client)
  const send = client.send.bind(client)
  client.get = async <T>(path: string, query?: Query): Promise<ApiResult<T>> => {
    if (path !== '/api/usage') return get<T>(path, query)
    usage.reads++
    if (usage.fail) throw new Error(usage.fail)
    return { data: structuredClone(usage.state) as T, eventId: null }
  }
  client.send = async <T>(method: Parameters<typeof send>[0], path: string, body?: unknown, o?: Parameters<typeof send>[3]): Promise<ApiResult<T>> => {
    if (path !== '/api/usage/refresh') return send<T>(method, path, body, o)
    refresh.calls++
    await refresh.gate
    if (refresh.refuse) throw new Error(refresh.refuse)
    return { data: structuredClone(refresh.state) as T, eventId: null }
  }
  return { ...hn, usage, refresh }
}
