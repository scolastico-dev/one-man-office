// The tabs under a ticket's description and fields. Each feature adds its tab
// with registerTicketTab; which apply follows the ticket.
import { shallowReactive, type Component } from 'vue'
import type { TicketSummary } from '../../../api/types'
import CommentThread from '../../comments/CommentThread.vue'
import AgentsTab from './AgentsTab.vue'
import DiffTab from './DiffTab.vue'
import TasksTab from './TasksTab.vue'

export interface TicketTab {
  id: string
  label: string
  /** Position among the tabs; lower first. */
  order: number
  applies(t: TicketSummary): boolean
  /** Shown with props `{ ticket: TicketSummary }`. */
  component: Component
  /** A number to show after the label; none when undefined. */
  count?(t: TicketSummary): number | undefined
}

const tabs = shallowReactive<TicketTab[]>([])

/** Adds a tab (a tab with the same id is replaced); returns the function that removes it. */
export function registerTicketTab(tab: TicketTab): () => void {
  const i = tabs.findIndex((x) => x.id === tab.id)
  if (i >= 0) tabs.splice(i, 1, tab)
  else tabs.push(tab)
  return () => {
    const at = tabs.indexOf(tab)
    if (at >= 0) tabs.splice(at, 1)
  }
}

/** The tabs that apply to the ticket, in order. */
export function ticketTabs(t: TicketSummary): TicketTab[] {
  return tabs.filter((x) => x.applies(t)).sort((a, b) => a.order - b.order)
}

const running = (t: TicketSummary) => {
  const n = t.agents.filter((a) => a.state !== 'ended').length
  return n > 0 ? n : undefined
}

registerTicketTab({ id: 'comments', label: 'Comments', order: 10, applies: () => true, component: CommentThread })
registerTicketTab({ id: 'diff', label: 'Diff', order: 20, applies: () => true, component: DiffTab })
registerTicketTab({ id: 'agents', label: 'Agents', order: 30, applies: () => true, component: AgentsTab, count: running })
registerTicketTab({
  id: 'tasks',
  label: 'Tasks',
  order: 40,
  applies: (t) => t.type === 'story',
  component: TasksTab,
  count: (t) => t.progress?.total,
})
