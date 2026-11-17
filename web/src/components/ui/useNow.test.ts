import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { afterEach, expect, test, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import RelativeTime from './RelativeTime.vue'

afterEach(() => vi.useRealTimers())

const list = (n: number) =>
  defineComponent({ setup: () => () => Array.from({ length: n }, () => h(RelativeTime, { at: '2026-11-17T09:58:00.000000000Z' })) })

test('every shown time reads one clock: 200 times run no more timers than one, and none once they are gone', async () => {
  vi.useFakeTimers({ now: Date.parse('2026-11-17T10:00:00Z') })
  const before = vi.getTimerCount()
  const one = mount(list(1))
  const withOne = vi.getTimerCount()
  expect(withOne).toBeGreaterThan(before)
  const many = mount(list(200))
  expect(vi.getTimerCount()).toBe(withOne)
  expect(many.text()).toContain('2 min ago')
  vi.advanceTimersByTime(60_000)
  await many.vm.$nextTick()
  expect(many.text()).toContain('3 min ago')
  expect(many.text()).not.toContain('2 min ago')
  one.unmount()
  many.unmount()
  vi.runOnlyPendingTimers()
  expect(vi.getTimerCount()).toBe(before)
})

test('no component runs a clock of its own; only the connection status counts seconds', () => {
  const SRC = join(__dirname, '../..')
  const files = (dir: string): string[] =>
    readdirSync(dir).flatMap((n) => {
      const p = join(dir, n)
      return statSync(p).isDirectory() ? files(p) : n.endsWith('.vue') ? [p] : []
    })
  const own = files(SRC)
    .filter((f) => /\bsetInterval\(/.test(readFileSync(f, 'utf8')))
    .map((f) => relative(SRC, f).split('\\').join('/'))
  expect(own).toEqual(['features/topbar/ConnectionStatus.vue'])
})
