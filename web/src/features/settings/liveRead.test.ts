import { flushPromises } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { effectScope, ref } from 'vue'
import type { ApiEvent } from '../../api/types'
import { liveRead } from './liveRead'

/** A store with hand-driven events, snapshots and connection. */
function fakeStore() {
  const events: ((ev: ApiEvent) => void)[] = []
  const snapshots: (() => void)[] = []
  const connection = ref<'live' | 'retrying' | 'closed' | 'connecting'>('live')
  return {
    connection,
    onEvent: (_kind: string, fn: (ev: ApiEvent) => void) => {
      events.push(fn)
      return () => events.splice(events.indexOf(fn), 1)
    },
    onSnapshot: (fn: () => void) => {
      snapshots.push(fn)
      return () => snapshots.splice(snapshots.indexOf(fn), 1)
    },
    event: (kind: string) => events.forEach((f) => f({ id: 1, kind, ticket: null, actor: { role: 'user' }, at: '', data: null })),
    snapshot: () => snapshots.forEach((f) => f()),
    listeners: () => events.length + snapshots.length,
  }
}

test('reads at once, and again on a watched event, a snapshot and when the stream is live again', async () => {
  const store = fakeStore()
  let n = 0
  const got: number[] = []
  const scope = effectScope()
  scope.run(() => liveRead({ store, read: async () => ++n, apply: (v) => got.push(v), watches: (ev) => ev.kind === 'mine' }))
  await flushPromises()
  store.event('other')
  store.event('mine')
  await flushPromises()
  store.snapshot()
  await flushPromises()
  store.connection.value = 'retrying'
  await flushPromises()
  store.connection.value = 'live'
  await flushPromises()
  expect(got).toEqual([1, 2, 3, 4])
  scope.stop()
  expect(store.listeners()).toBe(0)
})

test('an older answer never replaces a newer one; a failure of an older read is not shown', async () => {
  const store = fakeStore()
  const releases: ((v: string) => void)[] = []
  const fails: ((e: Error) => void)[] = []
  const got: string[] = []
  const errors: string[] = []
  const scope = effectScope()
  const r = scope.run(() =>
    liveRead({
      store,
      read: () =>
        new Promise<string>((res, rej) => {
          releases.push(res)
          fails.push(rej)
        }),
      apply: (v) => got.push(v),
      failed: (e) => errors.push((e as Error).message),
      watches: () => false,
    }),
  )!
  void r.reload()
  // The newer read answers first; the older one is dropped, also when it fails.
  releases[1]!('new')
  await flushPromises()
  releases[0]!('old')
  fails[0]!(new Error('old failure'))
  await flushPromises()
  expect(got).toEqual(['new'])
  expect(errors).toEqual([])
  scope.stop()
})
