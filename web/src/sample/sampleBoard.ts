// Static sample of a board in daily use, for judging the look without a
// running omo. Used only by the sample app.
import { COLUMNS, type AgentRef, type Column, type Project, type TicketProject, type TicketSummary } from '../api/types'
import { compareTime } from '../api/time'
import { badgeFor, captionFor } from '../board/badges'
import { COLUMN_TITLES, type CardModel, type ColumnModel } from '../features/board/model'

function project(id: string, name: string): Project {
  return {
    id,
    name,
    path: `/home/dev/src/${id}`,
    enabled: true,
    missing: false,
    root: '',
    story_merge: 'merge',
  }
}

export const sampleProjects: Project[] = [
  project('omo', 'one-man-office'),
  project('ui', 'github.com/not-three/ui'),
  project('api', 'github.com/not-three/api'),
  project('cli', 'github.com/not-three/cli'),
  project('infra', 'homelab-infrastructure-ansible-playbooks'),
]

function on(id: string, branch = ''): TicketProject {
  return { project: id, base_branch: 'main', branch, worktree: branch ? `/home/dev/.omo/worktrees/${branch}` : '' }
}

function agent(role: AgentRef['role'], name: string, state: AgentRef['state'] = 'running'): AgentRef {
  return { session: `${role}-${name}`, role, name, state }
}

let clock = Date.parse('2026-11-14T08:00:00Z')
function stamp(): string {
  clock += 37 * 60 * 1000
  return new Date(clock).toISOString()
}

function t(over: Partial<TicketSummary> & Pick<TicketSummary, 'number' | 'title' | 'column'>): TicketSummary {
  const created = stamp()
  return {
    type: 'task',
    resolution: '',
    rank: over.number,
    story: null,
    projects: [],
    supervisor: 'user',
    created_by: { role: 'user' },
    model: '',
    review: null,
    effective_review: 'agent',
    when_approved: null,
    effective_when_approved: 'merge',
    task_release: null,
    effective_task_release: null,
    max_parallel: null,
    review_stage: '',
    attention: '',
    export_merged: false,
    depends_on: [],
    blocks: [],
    waiting_for: [],
    open_questions: 0,
    questions_for_user: 0,
    progress: null,
    agents: [],
    description_version: 1,
    created_at: created,
    updated_at: created,
    closed_at: over.column === 'done' ? stamp() : null,
    ...over,
  }
}

const LONG_TITLE =
  'Investigate why the reviewer agent reports a passing end-to-end run although the Playwright report in the integration worktree shows two failed specs, and make the verdict depend on the exit code of the suite'

export const sampleTickets: TicketSummary[] = [
  // Planning
  t({
    number: 14,
    type: 'story',
    title: 'Mehrsprachige Oberfläche (Deutsch und Englisch)',
    column: 'planning',
    projects: [on('ui'), on('api'), on('cli')],
    progress: { done: 0, total: 0 },
    rank: 1,
  }),
  t({
    number: 15,
    title: 'Übersetzungsdateien für die Spaltennamen anlegen',
    column: 'planning',
    story: 14,
    projects: [on('ui')],
    supervisor: 'pm',
    created_by: { role: 'pm' },
    blocks: [16],
    rank: 1,
  }),
  t({
    number: 16,
    title: 'Language switch in the settings menu',
    column: 'planning',
    story: 14,
    projects: [on('ui')],
    supervisor: 'pm',
    depends_on: [15],
    rank: 2,
  }),
  t({ number: 30, title: 'Fix flaky recovery spec after a server restart', column: 'planning', projects: [on('omo')], rank: 3 }),
  t({ number: 31, title: LONG_TITLE, column: 'planning', projects: [on('omo')], rank: 4 }),
  t({ number: 47, title: 'Rename "freelancer" to "standalone task" in the docs', column: 'planning', projects: [on('omo')], rank: 5 }),

  // Ready For Dev
  t({
    number: 21,
    type: 'story',
    title: 'Export tickets as pull requests',
    column: 'ready',
    projects: [on('omo')],
    depends_on: [3],
    waiting_for: [{ ticket: 3, reason: 'not_done' }],
    progress: { done: 0, total: 0 },
    rank: 1,
  }),
  t({
    number: 11,
    title: 'Ticket card with badges and agent line',
    column: 'ready',
    story: 3,
    projects: [on('ui')],
    supervisor: 'pm',
    created_by: { role: 'pm' },
    depends_on: [10],
    waiting_for: [{ ticket: 10, reason: 'not_done' }],
    rank: 1,
  }),
  t({ number: 12, title: 'Column switcher for phones', column: 'ready', story: 3, projects: [on('ui')], supervisor: 'pm', start_blocked: 'slot', rank: 2 }),
  t({ number: 35, title: 'Add a health endpoint for the reverse proxy', column: 'ready', projects: [on('api')], busy: true, agents: [agent('developer', 'brisk-wren', 'starting')], rank: 3 }),
  t({
    number: 34,
    title: 'Upgrade Go toolchain to 1.26.2 in CI',
    column: 'ready',
    projects: [on('omo')],
    failed_step: 'start',
    attention: 'worktree could not be created: branch omo/34 already exists',
    rank: 4,
  }),
  t({ number: 36, title: 'Use the exported API client in the CLI', column: 'ready', projects: [on('cli')], depends_on: [29], waiting_for: [{ ticket: 29, reason: 'export_not_merged' }], rank: 5 }),
  t({ number: 38, title: 'Backup-Rotation für die Datenbank', column: 'ready', projects: [on('infra')], depends_on: [27], waiting_for: [{ ticket: 27, reason: 'cancelled' }], rank: 6 }),
  t({ number: 32, title: 'Dark mode for the login page', column: 'ready', projects: [on('ui')], start_blocked: 'paused', rank: 7 }),
  t({ number: 33, title: 'Summarise long agent logs', column: 'ready', projects: [on('omo')], start_blocked: 'no_model', rank: 8 }),
  t({ number: 37, title: 'Rate limit for the public paste endpoint', column: 'ready', projects: [on('api')], start_blocked: 'usage', rank: 9 }),

  // In Progress
  t({
    number: 3,
    type: 'story',
    title: 'Ticket board with live updates',
    column: 'in_progress',
    projects: [on('ui', 'omo/story-3'), on('api', 'omo/story-3')],
    progress: { done: 4, total: 9 },
    agents: [agent('pm', 'quiet-heron')],
    blocks: [21],
    rank: 1,
  }),
  t({
    number: 10,
    title: 'Event-Stream mit Wiederaufnahme nach Verbindungsabbruch',
    column: 'in_progress',
    story: 3,
    projects: [on('api', 'omo/10')],
    supervisor: 'pm',
    agents: [agent('developer', 'amber-otter')],
    blocks: [11],
    rank: 1,
  }),
  t({
    number: 13,
    title: 'Drag a card between columns',
    column: 'in_progress',
    story: 3,
    projects: [on('ui', 'omo/13')],
    supervisor: 'pm',
    agents: [agent('developer', 'calm-badger', 'waiting')],
    open_questions: 1,
    questions_for_user: 1,
    rank: 2,
  }),
  t({
    number: 40,
    title: 'Agent hat keine Schreibrechte im Worktree',
    column: 'in_progress',
    projects: [on('infra', 'omo/40')],
    agents: [agent('developer', 'tidy-moth', 'waiting')],
    attention: 'permission denied writing to /srv/backup; the agent stopped and waits for you',
    rank: 3,
  }),
  t({
    number: 41,
    title: 'Parse usage windows from the CLI status line',
    column: 'in_progress',
    projects: [on('omo', 'omo/41')],
    failed_step: 'agent',
    attention: 'the agent exited three times with code 1',
    rank: 4,
  }),
  t({ number: 42, title: 'Paste expiry in the share dialog', column: 'in_progress', projects: [on('ui', 'omo/42')], agents: [agent('developer', 'sly-gecko', 'starting')], rank: 5 }),

  // Review
  t({
    number: 8,
    type: 'story',
    title: 'Login token rotation',
    column: 'review',
    projects: [on('omo', 'omo/story-8')],
    progress: { done: 5, total: 5 },
    review_stage: 'agent',
    agents: [agent('reviewer', 'keen-lynx')],
    rank: 1,
  }),
  t({ number: 9, title: 'Snapshot loads open tickets and the 50 newest Done', column: 'review', story: 3, projects: [on('api', 'omo/9')], supervisor: 'pm', review_stage: 'human', effective_review: 'agent_then_human', rank: 1 }),
  t({ number: 43, title: 'Encrypt pastes on the client', column: 'review', projects: [on('ui', 'omo/43')], review_stage: 'merging', busy: true, rank: 2 }),
  t({
    number: 44,
    title: 'OpenAPI-Beschreibung der Paste-Endpunkte',
    column: 'review',
    projects: [on('api', 'omo/44')],
    review_stage: 'exporting',
    effective_when_approved: 'export',
    rank: 3,
  }),
  t({
    number: 45,
    title: 'Retry merge after a fast-forward race',
    column: 'review',
    projects: [on('omo', 'omo/45')],
    review_stage: 'human',
    failed_step: 'merge',
    attention: 'conflict in internal/board/move.go',
    rank: 4,
  }),
  t({ number: 46, title: 'Keyboard shortcuts help dialog', column: 'review', projects: [on('ui', 'omo/46')], review_stage: 'human', effective_review: 'human', rank: 5 }),

  // Done, oldest first; shown newest first
  t({ number: 1, type: 'story', title: 'Projekt-Grundgerüst und CI', column: 'done', resolution: 'completed', projects: [on('omo')], progress: { done: 3, total: 3 } }),
  t({ number: 2, title: 'Makefile targets for web and e2e', column: 'done', resolution: 'completed', story: 1, projects: [on('omo')] }),
  t({ number: 4, title: 'Snapshot endpoint for the board', column: 'done', resolution: 'completed', story: 3, projects: [on('api')] }),
  t({ number: 5, title: 'Design tokens and base components', column: 'done', resolution: 'completed', story: 3, projects: [on('ui')] }),
  t({ number: 27, title: 'Move backups to object storage', column: 'done', resolution: 'cancelled', projects: [on('infra')] }),
  t({ number: 6, title: 'Event id header on state changes', column: 'done', resolution: 'completed', story: 3, projects: [on('api')] }),
  t({ number: 29, title: 'Typed API client package', column: 'done', resolution: 'exported', projects: [on('api')], export_url: 'https://github.com/not-three/api/pull/112' }),
  t({ number: 7, title: 'API types and login token', column: 'done', resolution: 'completed', story: 3, projects: [on('ui')] }),
  t({ number: 48, title: 'Pin npm packages exactly', column: 'done', resolution: 'completed', projects: [on('omo')] }),
  t({ number: 49, title: 'Statusmeldung im Hub aktualisieren', column: 'done', resolution: 'completed', projects: [on('infra')] }),
]

function cardFor(x: TicketSummary): CardModel {
  const story = x.story === null ? undefined : sampleTickets.find((s) => s.number === x.story)
  return {
    ticket: x,
    storyTitle: story?.title,
    projectNames: x.projects.map((p) => sampleProjects.find((q) => q.id === p.project)?.name ?? p.project),
    badge: badgeFor(x),
    caption: captionFor(x),
    pending: false,
    refusedFlash: false,
  }
}

function byRank(a: TicketSummary, b: TicketSummary): number {
  return a.rank - b.rank || a.number - b.number
}

function order(column: Column): TicketSummary[] {
  const in_ = sampleTickets.filter((x) => x.column === column)
  if (column === 'done') {
    return in_.sort((a, b) => compareTime(b.closed_at ?? '', a.closed_at ?? '') || b.number - a.number)
  }
  return [...in_.filter((x) => x.type === 'story').sort(byRank), ...in_.filter((x) => x.type === 'task').sort(byRank)]
}

/** The ticket shown as dragged by the drag sample: a standalone task in Planning. */
export const SAMPLE_DRAGGED = 30

const DRAG_FROM_PLANNING: Record<Column, Pick<ColumnModel, 'dropState' | 'reason'>> = {
  planning: { dropState: 'allowed' },
  ready: { dropState: 'over' },
  in_progress: { dropState: 'refused', reason: 'A ticket enters In Progress when its agent starts or its review is rejected.' },
  review: { dropState: 'refused', reason: 'A ticket enters Review when its agent hands it in.' },
  done: { dropState: 'refused', reason: 'Only a review waiting for you can be dragged to Done. To close a ticket, cancel it.' },
}

export interface SampleOptions {
  /** No tickets at all, as on the first start. */
  empty?: boolean
  /** Show the board while #30 is dragged out of Planning. */
  drag?: boolean
}

export function sampleColumns(opts: SampleOptions = {}): ColumnModel[] {
  return COLUMNS.map((column) => {
    const cards = opts.empty ? [] : order(column).map(cardFor)
    return {
      column,
      title: COLUMN_TITLES[column],
      cards,
      count: cards.length,
      dropState: 'idle',
      more: column === 'done' && !opts.empty,
      ...(opts.drag ? DRAG_FROM_PLANNING[column] : {}),
    }
  })
}
