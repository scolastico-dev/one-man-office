// A board store over the sample data, so the parts of the top bar that read
// the store work on the sample page without a running omo. It never changes:
// requests that would change something are refused.
import { ApiError, type ApiClient, type ApiResult } from '../api/client'
import type { connectEventStream } from '../api/eventStream'
import type { General, ServerInfo, SettingsResponse, TicketList } from '../api/types'
import { createBoardStore, type BoardStore } from '../store'
import { sampleProjects, sampleTickets } from './sampleBoard'

const general: General = {
  max_running_agents: 4,
  ceo: { enabled: false, may_approve_stories: false },
  tasks: { require_user_release: false },
  review_defaults: { story: 'human', task: 'agent' },
  when_approved_defaults: { story: 'merge', task: 'merge' },
  waiting: { timeouts_before_investigate: 3 },
  health: { silent_after: '15m', question_unanswered_after: '1h' },
  stop: { handoff_timeout: '1m' },
  workspace_roots: [],
  rescan_interval: '1h',
  agent_env: {},
}

function answer(path: string): unknown {
  switch (path) {
    case '/api/tickets':
      return { tickets: sampleTickets, event_id: 1 } satisfies TicketList
    case '/api/projects':
      return sampleProjects
    case '/api/server':
      return { version: 'sample', paused: false, started_at: '2026-11-15T08:00:00Z', address: '' } satisfies ServerInfo
    case '/api/settings':
      return { settings: general, errors: [] } satisfies SettingsResponse
    default:
      throw new ApiError(404, 'not_found', 'The sample board has no data for this.')
  }
}

const client: ApiClient = {
  get: <T>(path: string) => Promise.resolve({ data: answer(path) as T, eventId: null } satisfies ApiResult<T>),
  send: () => Promise.reject(new ApiError(409, 'sample', 'The sample board does not change anything.')),
  getText: () => Promise.reject(new ApiError(409, 'sample', 'The sample board has no text answers.')),
  openStream: () => Promise.reject(new Error('the sample board has no event stream')),
}

/** Reports the stream as live and delivers nothing. */
const connect: typeof connectEventStream = (o) => {
  queueMicrotask(() => o.handlers.onState('live'))
  return { close() {}, reconnectNow() {} }
}

/** A started store over the sample tickets. */
export function createSampleStore(): BoardStore {
  const store = createBoardStore({ client, connect })
  void store.start()
  return store
}
