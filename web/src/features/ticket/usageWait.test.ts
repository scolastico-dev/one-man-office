import { expect, test } from 'vitest'
import type { ModelUsage, UsageState } from '../../api/types'
import { ticket } from '../board/testing'
import { untilWords, usageWaitText } from './usageWait'

const NOW = Date.parse('2026-11-15T20:00:00')
const at = (local: string) => new Date(local).toISOString()
const clock = (local: string) => new Date(local).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
const model = (id: string, state: ModelUsage['state'], resets: string | null = null): ModelUsage => ({ model: id, state, window: 'weekly', used_percent: 95, resets_at: resets === null ? null : at(resets) })
const usage = (...models: ModelUsage[]): UsageState => ({ readings: [], models })
const waiting = (model = '') => ticket({ number: 4, column: 'ready', start_blocked: 'usage', model })

test('until a clock time today, a weekday within the week, else a date', () => {
  expect(untilWords(at('2026-11-15T23:40:00'), NOW)).toBe(`until ${clock('2026-11-15T23:40:00')}`)
  const tomorrow = new Date('2026-11-16T09:15:00')
  expect(untilWords(at('2026-11-16T09:15:00'), NOW)).toBe(`until ${tomorrow.toLocaleDateString(undefined, { weekday: 'short' })} ${clock('2026-11-16T09:15:00')}`)
  const later = new Date('2026-11-30T09:15:00')
  expect(untilWords(at('2026-11-30T09:15:00'), NOW)).toBe(`until ${later.toLocaleDateString(undefined, { dateStyle: 'medium' })}`)
  expect(untilWords(null, NOW)).toBe('')
  expect(untilWords(at('2026-11-15T19:00:00'), NOW)).toBe('')
})

test("a ticket with its own model names it, says how full it is and until when, and what omo does next", () => {
  expect(usageWaitText(waiting('claude/opus'), usage(model('claude/opus', 'hard', '2026-11-15T23:40:00')), NOW)).toBe(
    `claude/opus is at its usage limit until ${clock('2026-11-15T23:40:00')}; omo starts this ticket when the model has room again.`,
  )
  expect(usageWaitText(waiting('claude/opus'), usage(model('claude/opus', 'soft', '2026-11-15T23:40:00')), NOW)).toBe(
    `claude/opus is near its usage limit until ${clock('2026-11-15T23:40:00')}; omo starts this ticket when the model has room again, or now with Start anyway.`,
  )
  // Without a reading of the model: what is known.
  expect(usageWaitText(waiting('claude/opus'), null, NOW)).toBe('claude/opus is near its usage limit; omo starts this ticket when the model has room again, or now with Start anyway.')
})

test('a ticket without a model of its own names the full models and waits for one of its role', () => {
  expect(usageWaitText(waiting(), usage(model('claude/opus', 'hard', '2026-11-15T23:40:00'), model('claude/haiku', 'ok')), NOW)).toBe(
    `claude/opus is at its usage limit until ${clock('2026-11-15T23:40:00')}; omo starts this ticket when a model of its role has room.`,
  )
  expect(usageWaitText(waiting(), usage(model('claude/opus', 'hard'), model('claude/sonnet', 'soft', '2026-11-15T22:10:00'), model('codex/default', 'soft')), NOW)).toBe(
    `claude/opus is at its usage limit, claude/sonnet is near its usage limit until ${clock('2026-11-15T22:10:00')} and codex/default is near its usage limit; omo starts this ticket when a model of its role has room.`,
  )
  expect(usageWaitText(waiting(), usage(model('claude/haiku', 'ok')), NOW)).toBe('The models of its role are near their usage limits; omo starts this ticket when one has room.')
})

test('nothing for a ticket that does not wait for usage', () => {
  expect(usageWaitText(ticket({ start_blocked: 'slot' }), null, NOW)).toBe('')
})
