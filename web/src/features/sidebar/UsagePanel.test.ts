import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { UsageState } from '../../api/types'
import UsagePanel from './UsagePanel.vue'
import { mountSidebar } from './sidebarTesting'
import { reading, usageHarness } from './usageTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

async function open(state: UsageState) {
  const hn = await usageHarness(state)
  const { wrapper, router } = await mountSidebar(hn, UsagePanel)
  mounted = wrapper
  await flushPromises()
  return { hn, w: wrapper, router }
}

const soon = new Date(Date.now() + 3 * 3600_000 + 60_000).toISOString()

test('shows a meter per window with the effective thresholds, states in words and the models kept off', async () => {
  const { w } = await open({
    readings: [
      reading({
        profiles: ['claude', 'claude-review'],
        windows: [
          { name: 'session', label: '', used_percent: 42.5, resets_at: soon },
          { name: 'weekly', label: '', used_percent: 91.2, resets_at: null },
        ],
      }),
    ],
    models: [
      { model: 'claude/fable', state: 'hard', window: 'weekly', used_percent: 99, resets_at: soon, note: '' },
      { model: 'claude/opus', state: 'soft', window: 'weekly', used_percent: 91, resets_at: null, note: '' },
      { model: 'claude/haiku', state: 'ok', window: 'session', used_percent: 4, resets_at: null, note: '' },
    ],
  })
  expect(w.get('[data-usage-account]').text()).toContain('claude, claude-review')
  expect(w.get('[data-usage-account]').text()).toContain('updated')
  const meters = w.findAll('[role="meter"]')
  expect(meters).toHaveLength(2)
  // The general thresholds of the fake omo are 80 and 95.
  expect(w.get('[data-usage-window="weekly"]').text()).toContain('91 %')
  expect(w.get('[data-usage-window="weekly"]').text()).toContain('near its limit')
  expect(w.get('[data-usage-window="weekly"]').text()).toContain('waits at 80 %, stops at 95 %')
  expect(w.get('[data-usage-window="session"]').text()).toContain('resets in 3 h')
  const limited = w.get('[data-usage-limited]').text()
  expect(limited).toContain('claude/fable — at its limit, resets in 3 h')
  expect(limited).toContain('claude/opus — near its limit')
  expect(limited).not.toContain('haiku')
})

test('a failed script shows its error as text and says its models are available', async () => {
  const { w } = await open({
    readings: [reading({ windows: [], fetched_at: null, error: 'line 23: <b>http</b> request failed.' })],
    models: [],
  })
  const err = w.get('[data-usage-script-error]')
  expect(err.text()).toBe('Script failed: line 23: <b>http</b> request failed. Its models count as available.')
  expect(err.find('b').exists()).toBe(false)
  expect(w.text()).toContain('not read yet')
  expect(w.find('[data-usage-limited]').exists()).toBe(false)
})

test('after a failed run the meters keep their last values for 30 minutes, said to be old, as the server judges its models', async () => {
  const fetched = new Date(Date.now() - 12 * 60_000)
  const since = new Date(Date.now() - 5 * 60_000)
  const day = (t: Date) => (t.toDateString() === new Date().toDateString() ? '' : `${t.toLocaleDateString(undefined, { weekday: 'short' })} `)
  const clock = (t: Date) => `${day(t)}${t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}`
  const { w } = await open({
    readings: [
      reading({
        windows: [{ name: 'weekly', label: '', used_percent: 96, resets_at: soon }],
        fetched_at: fetched.toISOString(),
        error: 'the usage API is rate limited; trying again later',
        stale_since: since.toISOString(),
      }),
    ],
    models: [{ model: 'claude/opus', state: 'hard', window: 'weekly', used_percent: 96, resets_at: soon, note: '' }],
  })
  const meter = w.get('[data-usage-window="weekly"]')
  expect(meter.get('[data-state]').attributes('data-state')).toBe('hard')
  expect(meter.get('[data-usage-value]').text()).toBe('96 %')
  expect(w.get('[data-usage-stale]').text()).toBe(
    `Old readings: as of ${clock(fetched)}; the last refresh failed: the usage API is rate limited; trying again later. ` +
      `They count until ${clock(new Date(since.getTime() + 30 * 60_000))}, then its models are unknown.`,
  )
  expect(w.find('[data-usage-script-error]').exists()).toBe(false)
  expect(w.get('[data-usage-limited]').text()).toContain('claude/opus — at its limit')
})

test('30 minutes after the first failed run the meters say unknown, without the old number', async () => {
  const { w } = await open({
    readings: [
      reading({
        windows: [{ name: 'weekly', label: '', used_percent: 96, resets_at: soon }],
        error: 'no readings',
        stale_since: new Date(Date.now() - 31 * 60_000).toISOString(),
      }),
    ],
    models: [{ model: 'claude/opus', state: 'unknown', window: '', used_percent: null, resets_at: null, note: '' }],
  })
  const meter = w.get('[data-usage-window="weekly"]')
  expect(meter.get('[data-state]').attributes('data-state')).toBe('unknown')
  expect(meter.get('[data-usage-value]').text()).toBe('?')
  expect(meter.text()).not.toContain('near its limit')
  expect(meter.get('[role="meter"]').attributes('aria-valuetext')).toBe('unknown')
  expect(w.find('[data-usage-stale]').exists()).toBe(false)
  expect(w.get('[data-usage-script-error]').text()).toBe('Script failed: no readings. Its models count as available.')
  expect(w.find('[data-usage-limited]').exists()).toBe(false)
})

test("shows each model's note on what its reading lacks", async () => {
  const { w } = await open({
    readings: [reading()],
    models: [
      { model: 'claude/fable', state: 'unknown', window: '', used_percent: null, resets_at: null, note: 'claude/fable counts the window fable_weekly; the script did not report it, so its usage is unknown' },
      { model: 'claude/opus', state: 'ok', window: 'session', used_percent: 42.5, resets_at: null, note: '' },
    ],
  })
  const notes = w.findAll('[data-usage-note]')
  expect(notes.map((n) => n.text())).toEqual(['claude/fable counts the window fable_weekly; the script did not report it, so its usage is unknown.'])
  expect(notes[0]!.attributes('data-usage-note')).toBe('claude/fable')
})

test('a reading older than 24 hours is unknown', async () => {
  const { w } = await open({
    readings: [reading({ windows: [{ name: 'weekly', label: '', used_percent: 96, resets_at: null }], fetched_at: new Date(Date.now() - 25 * 3600_000).toISOString() })],
    models: [],
  })
  expect(w.get('[data-usage-window="weekly"] [data-state]').attributes('data-state')).toBe('unknown')
})

test('a window past its reset counts 0 %', async () => {
  const { w } = await open({
    readings: [reading({ windows: [{ name: 'weekly', label: '', used_percent: 99, resets_at: new Date(Date.now() - 60_000).toISOString() }] })],
    models: [],
  })
  const meter = w.get('[data-usage-window="weekly"]')
  expect(meter.get('[data-state]').attributes('data-state')).toBe('ok')
  expect(meter.get('[data-usage-value]').text()).toBe('0 %')
  expect(meter.find('[data-usage-reset]').exists()).toBe(false)
})

test('says so when no profile reports usage, and links to the settings', async () => {
  const { w, router } = await open({ readings: [], models: [] })
  expect(w.get('[data-usage-none]').text()).toBe('No profile reports usage.')
  expect(w.find('[data-usage-refresh]').exists()).toBe(false)
  await w.get('[data-usage-settings]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/settings/usage')
})

test('Refresh is busy while it runs and shows a refusal', async () => {
  const { hn, w } = await open({ readings: [reading()], models: [] })
  let release!: () => void
  hn.refresh.gate = new Promise<void>((r) => (release = r))
  hn.refresh.refuse = 'a refresh is already running'
  const button = w.get('[data-usage-refresh]')
  await button.trigger('click')
  await flushPromises()
  expect(button.text()).toBe('Refreshing…')
  expect(button.attributes('disabled')).toBeDefined()
  release()
  await flushPromises()
  expect(button.text()).toBe('Refresh')
  expect(w.get('[data-usage-error]').text()).toBe('a refresh is already running')
})

test('a window with a label shows the label, with its name as the tooltip; one without shows its name', async () => {
  const { w } = await open({
    readings: [
      reading({
        windows: [
          { name: 'fable_weekly', label: 'Weekly, Fable', used_percent: 43, resets_at: null },
          { name: 'extra_usage', label: 'Extra usage credits (1.32 of 5.00 EUR)', used_percent: 26.4, resets_at: null },
          { name: 'weekly', label: '', used_percent: 85, resets_at: null },
          { name: 'odd', label: 'Week‮ly', used_percent: 1, resets_at: null },
        ],
      }),
    ],
    models: [],
  })
  const label = (name: string) => w.get(`[data-usage-window="${name}"] .umeter__label`)
  expect(label('fable_weekly').text()).toBe('Weekly, Fable')
  expect(label('fable_weekly').attributes('title')).toBe('fable_weekly')
  expect(w.get('[data-usage-window="fable_weekly"] [role="meter"]').attributes('aria-labelledby')).toBe(label('fable_weekly').attributes('id'))
  expect(label('extra_usage').text()).toBe('Extra usage credits (1.32 of 5.00 EUR)')
  expect(label('weekly').text()).toBe('weekly')
  expect(label('weekly').attributes('title')).toBeUndefined()
  expect(label('odd').text()).toBe('Week\\u{202E}ly')
})
