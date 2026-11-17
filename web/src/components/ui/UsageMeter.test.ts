import { enableAutoUnmount, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import UsageMeter, { resetWords } from './UsageMeter.vue'

// The shown times share one clock (useNow): a view left mounted would keep it running into the next test.
enableAutoUnmount(afterEach)

const NOW = '2026-11-15T12:00:30Z'

describe('UsageMeter', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(NOW))
  })
  afterEach(() => vi.useRealTimers())

  test("a window shown by its label has its technical name as the label's tooltip", () => {
    const labelled = mount(UsageMeter, { props: { label: 'Weekly, Fable', name: 'fable_weekly', percent: 43 } })
    const label = labelled.get(`#${labelled.get('[role="meter"]').attributes('aria-labelledby')}`)
    expect(label.text()).toBe('Weekly, Fable')
    expect(label.attributes('title')).toBe('fable_weekly')
    const plain = mount(UsageMeter, { props: { label: 'weekly', name: 'weekly', percent: 43 } })
    expect(plain.get('.umeter__label').attributes('title')).toBeUndefined()
    const odd = mount(UsageMeter, { props: { label: 'x', name: 'a\u202Eb', percent: 43 } })
    expect(odd.get('.umeter__label').attributes('title')).toBe('a\\u{202E}b')
  })

  test('meter says its value and state in words', () => {
    const wrapper = mount(UsageMeter, {
      props: { label: 'weekly', percent: 91.6, soft: 90, hard: 98, resetsAt: '2026-11-15T15:10:00Z' },
    })
    const meter = wrapper.get('[role="meter"]')
    expect(meter.attributes('aria-valuemin')).toBe('0')
    expect(meter.attributes('aria-valuemax')).toBe('100')
    expect(meter.attributes('aria-valuenow')).toBe('91.6')
    expect(meter.attributes('aria-valuetext')).toBe('91 % used, near its limit, resets in 3 h 9 min')
    // Named by its visible label.
    const label = wrapper.get(`#${meter.attributes('aria-labelledby')}`)
    expect(label.text()).toBe('weekly')
    expect(wrapper.get('[data-usage-value]').text()).toBe('91 %')
    expect(wrapper.get('[data-usage-state]').text()).toBe('near its limit')
    expect(wrapper.attributes('data-state')).toBe('soft')
  })

  test('the state follows the thresholds unless the server names it', async () => {
    const wrapper = mount(UsageMeter, { props: { label: 'session', percent: 42, soft: 90, hard: 98 } })
    expect(wrapper.attributes('data-state')).toBe('ok')
    expect(wrapper.find('[data-usage-state]').exists()).toBe(false)
    expect(wrapper.get('[role="meter"]').attributes('aria-valuetext')).toBe('42 % used')
    await wrapper.setProps({ percent: 98 })
    expect(wrapper.get('[data-usage-state]').text()).toBe('at its limit')
    await wrapper.setProps({ percent: 42, state: 'hard' })
    expect(wrapper.get('[data-usage-state]').text()).toBe('at its limit')
    // Over 100 is shown full; the bar never runs past its end.
    await wrapper.setProps({ percent: 130, state: undefined })
    expect(wrapper.get('[role="meter"]').attributes('aria-valuenow')).toBe('100')
    expect(wrapper.get('[data-usage-fill]').attributes('style')).toContain('width: 100%')
  })

  test('unknown has no value and says unknown', () => {
    const wrapper = mount(UsageMeter, { props: { label: 'session', percent: null, soft: 90, hard: 98 } })
    const meter = wrapper.get('[role="meter"]')
    // A meter must carry a value; the text in words is what is read out.
    expect(meter.attributes('aria-valuenow')).toBe('0')
    expect(meter.attributes('aria-valuetext')).toBe('unknown')
    expect(wrapper.get('[data-usage-value]').text()).toBe('?')
    expect(wrapper.get('[data-usage-state]').text()).toBe('unknown')
    expect(wrapper.find('[data-usage-fill]').exists()).toBe(false)
  })

  test('an unmetered model says so and has no value', () => {
    const wrapper = mount(UsageMeter, { props: { label: 'gemini/default', percent: null, state: 'unmetered' } })
    const meter = wrapper.get('[role="meter"]')
    expect(meter.attributes('aria-valuenow')).toBe('0')
    expect(meter.attributes('aria-valuetext')).toBe('not metered')
    expect(wrapper.get('[data-usage-state]').text()).toBe('not metered')
  })

  test('marks at soft and hard', () => {
    const wrapper = mount(UsageMeter, { props: { label: 'session', percent: 42, soft: 80, hard: 95 } })
    expect(wrapper.get('[data-mark="soft"]').attributes('style')).toContain('left: 80%')
    expect(wrapper.get('[data-mark="hard"]').attributes('style')).toContain('left: 95%')
    // The marks are explained in words, not only drawn.
    expect(wrapper.get('[data-usage-limits]').text()).toBe('waits at 80 %, stops at 95 %')
    const plain = mount(UsageMeter, { props: { label: 'session', percent: 42 } })
    expect(plain.find('[data-mark]').exists()).toBe(false)
    expect(plain.find('[data-usage-limits]').exists()).toBe(false)
  })

  test('reset time shown relative', async () => {
    const wrapper = mount(UsageMeter, { props: { label: 'session', percent: 42, resetsAt: '2026-11-15T12:20:00Z' } })
    const time = wrapper.get('[data-usage-reset] time')
    expect(time.attributes('datetime')).toBe('2026-11-15T12:20:00Z')
    expect(time.attributes('title')).toBe(new Date('2026-11-15T12:20:00Z').toLocaleString(undefined, { dateStyle: 'full', timeStyle: 'medium' }))
    expect(wrapper.get('[data-usage-reset]').text()).toBe('resets in 19 min')
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    expect(wrapper.get('[data-usage-reset]').text()).toBe('resets in 9 min')
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })

  test('reset words: minutes, hours, days, a date, and a time already passed', () => {
    const now = Date.parse(NOW)
    expect(resetWords('2026-11-15T12:00:50Z', now)).toBe('resets in under a minute')
    // Below ten hours the minutes count too: 1 h 59 min is not "1 h".
    expect(resetWords('2026-11-15T13:59:30Z', now)).toBe('resets in 1 h 59 min')
    expect(resetWords('2026-11-15T15:10:00Z', now)).toBe('resets in 3 h 9 min')
    expect(resetWords('2026-11-15T15:00:40Z', now)).toBe('resets in 3 h')
    expect(resetWords('2026-11-15T21:59:40Z', now)).toBe('resets in 9 h 59 min')
    expect(resetWords('2026-11-15T22:30:00Z', now)).toBe('resets in 10 h')
    expect(resetWords('2026-11-16T13:00:00Z', now)).toBe('resets in 1 day')
    expect(resetWords('2026-11-18T13:00:00Z', now)).toBe('resets in 3 days')
    expect(resetWords('2026-12-01T12:00:00Z', now)).toBe(
      `resets on ${new Date('2026-12-01T12:00:00Z').toLocaleDateString(undefined, { dateStyle: 'medium' })}`,
    )
    expect(resetWords('2026-11-15T11:00:00Z', now)).toBe('resets now')
    // Server times with nine digits of fraction and offsets parse too; other text is left out.
    expect(resetWords('2026-11-15T14:30:30.123456789+02:00', now)).toBe('resets in 30 min')
    expect(resetWords('soon', now)).toBe('')
  })
})
