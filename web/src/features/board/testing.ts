// Builders for component tests of the board.
import type { Column, TicketSummary } from '../../api/types'
import { COLUMN_TITLES, type CardModel, type ColumnModel } from './model'

export function ticket(over: Partial<TicketSummary> = {}): TicketSummary {
  return {
    number: 1,
    type: 'task',
    title: 'A ticket',
    column: 'planning',
    resolution: '',
    rank: 1,
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
    busy: false,
    failed_step: '',
    start_blocked: '',
    gate: null,
    export_url: '',
    export_merged: false,
    workdir: '',
    depends_on: [],
    blocks: [],
    waiting_for: [],
    open_questions: 0,
    questions_for_user: 0,
    progress: null,
    agents: [],
    description_version: 1,
    created_at: '2026-11-01T10:00:00Z',
    updated_at: '2026-11-01T10:00:00Z',
    closed_at: null,
    ...over,
  }
}

export function card(over: Partial<TicketSummary> = {}, extra: Partial<CardModel> = {}): CardModel {
  return { ticket: ticket(over), projectNames: [], badge: null, pending: false, refusedFlash: false, ...extra }
}

export function column(c: Column, cards: CardModel[] = [], extra: Partial<ColumnModel> = {}): ColumnModel {
  return { column: c, title: COLUMN_TITLES[c], cards, count: cards.length, dropState: 'idle', ...extra }
}
