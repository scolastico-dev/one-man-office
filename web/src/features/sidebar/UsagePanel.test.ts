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
          { name: 'session', used_percent: 42.5, resets_at: soon },
          { name: 'weekly', used_percent: 91.2, resets_at: null },
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

test('after a failed run the meters say unknown, without the old number, as the server judges its models', async () => {
  const { w } = await open({
    readings: [reading({ windows: [{ name: 'weekly', used_percent: 96, resets_at: soon }], error: 'no readings' })],
    models: [{ model: 'claude/opus', state: 'unknown', window: '', used_percent: null, resets_at: null, note: '' }],
  })
  const meter = w.get('[data-usage-window="weekly"]')
  expect(meter.get('[data-state]').attributes('data-state')).toBe('unknown')
  expect(meter.get('[data-usage-value]').text()).toBe('?')
  expect(meter.text()).not.toContain('near its limit')
  expect(meter.get('[role="meter"]').attributes('aria-valuetext')).toBe('unknown')
  expect(w.find('[data-usage-limited]').exists()).toBe(false)
})

test('a reading older than 24 hours is unknown', async () => {
  const { w } = await open({
    readings: [reading({ windows: [{ name: 'weekly', used_percent: 96, resets_at: null }], fetched_at: new Date(Date.now() - 25 * 3600_000).toISOString() })],
    models: [],
  })
  expect(w.get('[data-usage-window="weekly"] [data-state]').attributes('data-state')).toBe('unknown')
})

test('a window past its reset counts 0 %', async () => {
  const { w } = await open({
    readings: [reading({ windows: [{ name: 'weekly', used_percent: 99, resets_at: new Date(Date.now() - 60_000).toISOString() }] })],
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
