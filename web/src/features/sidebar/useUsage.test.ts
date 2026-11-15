import { flushPromises } from '@vue/test-utils'
import { effectScope } from 'vue'
import { afterEach, expect, test } from 'vitest'
import type { UsageState } from '../../api/types'
import { createUsage, type Usage } from './useUsage'
import { reading, usageHarness } from './usageTesting'

let stop: (() => void) | null = null
afterEach(() => {
  stop?.()
  stop = null
})

const withReading = (percent: number): UsageState => ({ readings: [reading({ windows: [{ name: 'session', used_percent: percent, resets_at: null }] })], models: [] })
const percentOf = (u: Usage) => u.state.value?.readings[0]?.windows[0]?.used_percent

async function open(first?: UsageState) {
  const hn = await usageHarness(first)
  const shared = createUsage({ client: hn.session.client, store: hn.session.store })
  const scope = effectScope()
  let release = () => {}
  scope.run(() => {
    release = shared.acquire()
  })
  stop = () => {
    release()
    scope.stop()
  }
  await flushPromises()
  return { hn, usage: shared.usage, shared }
}

test('reads the state first and applies usage.changed without a read', async () => {
  const { hn, usage } = await open(withReading(10))
  expect(usage.loaded.value).toBe(true)
  expect(percentOf(usage)).toBe(10)
  const reads = hn.usage.reads
  hn.omo.emit('usage.changed', withReading(96))
  expect(percentOf(usage)).toBe(96)
  expect(hn.usage.reads).toBe(reads)
})

test('re-reads on a snapshot and shows a read that failed', async () => {
  const { hn, usage } = await open(withReading(10))
  hn.usage.state = withReading(55)
  hn.usage.fail = 'omo is busy'
  // The store reads its snapshot after a resync, and the usage is read once that is installed.
  hn.omo.stream!.onResync(hn.omo.eventId)
  await flushPromises()
  expect(usage.error.value).toBe('omo is busy')
  expect(percentOf(usage)).toBe(10)
  hn.usage.fail = ''
  hn.omo.stream!.onResync(hn.omo.eventId)
  await flushPromises()
  expect(percentOf(usage)).toBe(55)
  expect(usage.error.value).toBe('')
})

test('an older answer never replaces a newer state', async () => {
  const { hn, usage } = await open(withReading(10))
  let release!: () => void
  hn.refresh.gate = new Promise<void>((r) => (release = r))
  hn.refresh.state = withReading(20)
  const pending = usage.refresh()
  await flushPromises()
  expect(usage.refreshing.value).toBe(true)
  // The event is newer than the refresh that is still under way.
  hn.omo.emit('usage.changed', withReading(97))
  release()
  await pending
  expect(percentOf(usage)).toBe(97)
  expect(usage.refreshing.value).toBe(false)
})

test('refresh shows the refusal and runs once at a time', async () => {
  const { hn, usage } = await open(withReading(10))
  hn.refresh.refuse = 'a refresh is already running'
  await Promise.all([usage.refresh(), usage.refresh()])
  expect(hn.refresh.calls).toBe(1)
  expect(usage.error.value).toBe('a refresh is already running')
  hn.refresh.refuse = ''
  hn.refresh.state = withReading(12)
  await usage.refresh()
  expect(usage.error.value).toBe('')
  expect(percentOf(usage)).toBe(12)
})

test('released with its last user: no read and no event is taken afterwards', async () => {
  const { hn, usage, shared } = await open(withReading(10))
  stop?.()
  stop = null
  hn.omo.emit('usage.changed', withReading(99))
  expect(percentOf(usage)).toBe(10)
  const reads = hn.usage.reads
  hn.omo.stream!.onResync(hn.omo.eventId)
  await flushPromises()
  expect(hn.usage.reads).toBe(reads)
  // Used again, it starts over and reads.
  const again = effectScope()
  again.run(() => shared.acquire())
  await flushPromises()
  expect(percentOf(usage)).toBe(10)
  expect(hn.usage.reads).toBe(reads + 1)
  again.stop()
})
