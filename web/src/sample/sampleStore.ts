// A board store over the sample data, so the parts of the top bar that read
// the store work on the sample page without a running omo. It never changes:
// requests that would change something are refused.
import { ApiError, type ApiClient, type ApiResult } from '../api/client'
import type { connectEventStream } from '../api/eventStream'
import type { General, ServerInfo, SettingsResponse, Ticket, TicketList } from '../api/types'
import { createBoardStore, type BoardStore } from '../store'
import { sampleDescription, sampleProjects, sampleTickets } from './sampleBoard'

const general: General = {
  limits: { tasks: 4, stories: 2 },
  ceo: { enabled: false, may_approve_stories: false },
  tasks: { require_user_release: false },
  ui: { open_tickets_as_page: false },
  branches: { template: 'omo/{slug}' },
  review_defaults: { story: 'human', task: 'agent', story_task: 'agent' },
  when_approved_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
  merge_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
  waiting: { timeouts_before_investigate: 3 },
  health: { silent_after: '15m', question_unanswered_after: '1h' },
  images: { keep_days_after_done: 60 },
  stop: { handoff_timeout: '1m' },
  workspace_roots: [],
  rescan_interval: '1h',
  agent_env: {},
  usage: { soft_percent: 80, hard_percent: 95, refresh_interval: '5m', windows: {} },
}

function answer(path: string): unknown {
  switch (path) {
    case '/api/tickets':
      return { tickets: sampleTickets, event_id: 1 } satisfies TicketList
    case '/api/projects':
      return sampleProjects
    case '/api/server':
      return { version: 'sample', paused: false, started_at: '2026-11-15T08:00:00Z', address: '', address_configured: false, listen: '', version_url: '', version_commit: '' } satisfies ServerInfo
    case '/api/settings':
      return { settings: general, errors: [], revision: '0', home: '/home/sample/.local/omo' } satisfies SettingsResponse
    case '/api/models':
      return []
    default: {
      // A ticket's panel reads the ticket with its description.
      const m = /^\/api\/tickets\/(\d+)$/.exec(path)
      const ticket = m ? sampleTickets.find((x) => x.number === Number(m[1])) : undefined
      if (ticket) return { ...ticket, description: sampleDescription(ticket.number) } satisfies Ticket
      throw new ApiError(404, 'not_found', 'The sample board has no data for this.')
    }
  }
}

/** Answers reads from the sample data and refuses every change. */
export const sampleClient: ApiClient = {
  get: <T>(path: string) => Promise.resolve({ data: answer(path) as T, eventId: null } satisfies ApiResult<T>),
  send: () => Promise.reject(new ApiError(409, 'sample', 'The sample board does not change anything.')),
  getText: () => Promise.reject(new ApiError(409, 'sample', 'The sample board has no text answers.')),
  getBlob: () => Promise.reject(new ApiError(404, 'not_found', 'The sample board has no images.')),
  upload: () => Promise.reject(new ApiError(409, 'sample', 'The sample board does not change anything.')),
  openStream: () => Promise.reject(new Error('the sample board has no event stream')),
}

/** Reports the stream as live and delivers nothing. */
const connect: typeof connectEventStream = (o) => {
  queueMicrotask(() => o.handlers.onState('live'))
  return { close() {}, reconnectNow() {} }
}

/** A started store over the sample tickets. */
export function createSampleStore(): BoardStore {
  const store = createBoardStore({ client: sampleClient, connect })
  void store.start()
  return store
}
