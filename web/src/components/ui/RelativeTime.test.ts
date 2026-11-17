import { enableAutoUnmount, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import RelativeTime from './RelativeTime.vue'

// The shown times share one clock (useNow): a view left mounted would keep it running into the next test.
enableAutoUnmount(afterEach)

const FULL: Intl.DateTimeFormatOptions = { dateStyle: 'full', timeStyle: 'medium' }

describe('RelativeTime', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-15T12:00:30Z'))
  })
  afterEach(() => vi.useRealTimers())

  test('shows minutes and the full local time in the title', () => {
    // The server writes nine digits of fraction; that must still parse.
    const at = '2026-11-15T11:57:00.123456789Z'
    const wrapper = mount(RelativeTime, { props: { at } })
    const time = wrapper.get('time')
    expect(time.attributes('datetime')).toBe(at)
    expect(time.text()).toBe('3 min ago')
    expect(time.attributes('title')).toBe(new Date('2026-11-15T11:57:00.123Z').toLocaleString(undefined, FULL))
  })

  test('updates each minute', async () => {
    const wrapper = mount(RelativeTime, { props: { at: '2026-11-15T12:00:00Z' } })
    expect(wrapper.text()).toBe('just now')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(wrapper.text()).toBe('1 min ago')
    await vi.advanceTimersByTimeAsync(60 * 60_000)
    expect(wrapper.text()).toBe('1 h ago')
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })

  test('hours, days, and an offset time compared by value', () => {
    const text = (at: string) => mount(RelativeTime, { props: { at } }).text()
    expect(text('2026-11-15T09:00:00Z')).toBe('3 h ago')
    expect(text('2026-11-15T14:00:00+02:00')).toBe('just now')
    expect(text('2026-11-13T12:00:00Z')).toBe('2 days ago')
    expect(text('2026-11-14T11:00:00Z')).toBe('1 day ago')
  })

  test('long ago shows the local date; text that is no time is shown as it is', () => {
    const old = mount(RelativeTime, { props: { at: '2026-09-01T08:00:00Z' } })
    expect(old.text()).toBe(new Date('2026-09-01T08:00:00Z').toLocaleDateString(undefined, { dateStyle: 'medium' }))
    const bad = mount(RelativeTime, { props: { at: 'yesterday-ish' } })
    expect(bad.text()).toBe('yesterday-ish')
    expect(bad.get('time').attributes('title')).toBeUndefined()
  })

  test('a time slightly ahead of this clock reads as just now', () => {
    expect(mount(RelativeTime, { props: { at: '2026-11-15T12:01:00Z' } }).text()).toBe('just now')
  })
})
