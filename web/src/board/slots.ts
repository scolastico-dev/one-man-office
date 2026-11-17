import type { SlotLoad, TicketSummary } from '../api/types'

/** The rule of the two work limits in words, for the Limits page and its sample. */
export const SLOT_RULE =
  'A task or story takes a slot when it moves to In Progress and gives it back when it is Done; waiting, review and questions do not free it. The CEO never counts. A story can also limit its own parallel tasks in its panel. Lowering a limit stops nothing; new work waits until fewer are in work. Applies at the next scheduling pass.'

/**
 * How many tasks and stories are in work against their limits, and per
 * story how many of its tasks are, counted from the board the way the engine
 * counts: a ticket holds a slot of its kind from In Progress until Done (in
 * In Progress and Review, and while a start claims it in Ready For Dev).
 */
export interface SlotView {
  tasks: SlotLoad
  stories: SlotLoad
  /** Per story number, its tasks in work. */
  perStory: ReadonlyMap<number, number>
  /** Per story number, its own limit of parallel tasks (only stories that set one). */
  maxParallel: ReadonlyMap<number, number>
}

/** Whether t holds a slot of its kind. */
export function holdsSlot(t: Pick<TicketSummary, 'column' | 'busy'>): boolean {
  return t.column === 'in_progress' || t.column === 'review' || (t.column === 'ready' && t.busy)
}

/** The slot view of the loaded tickets under the limits; null until the limits are known. */
export function slotView(tickets: Iterable<TicketSummary>, limits: { tasks: number; stories: number } | null | undefined): SlotView | null {
  if (!limits) return null
  const tasks: SlotLoad = { in_work: 0, limit: limits.tasks }
  const stories: SlotLoad = { in_work: 0, limit: limits.stories }
  const perStory = new Map<number, number>()
  const maxParallel = new Map<number, number>()
  for (const t of tickets) {
    if (t.type === 'story' && t.max_parallel !== null && t.max_parallel > 0) maxParallel.set(t.number, t.max_parallel)
    if (!holdsSlot(t)) continue
    if (t.type === 'story') {
      stories.in_work++
      continue
    }
    tasks.in_work++
    if (t.story !== null) perStory.set(t.story, (perStory.get(t.story) ?? 0) + 1)
  }
  return { tasks, stories, perStory, maxParallel }
}
