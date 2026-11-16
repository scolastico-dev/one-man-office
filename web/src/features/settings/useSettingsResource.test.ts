import { flushPromises } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ref } from 'vue'
import { ApiError } from '../../api/client'
import type { StreamState } from '../../api/eventStream'
import type { ApiEvent, FileError } from '../../api/types'
import {
  BLOCKED_TEXT,
  BROKEN_QUESTION,
  CHOOSE_FIRST_TEXT,
  createSettingsResource,
  diskChangeText,
  LOCKED_TEXT,
  LOST_TEXT,
  sameJSON,
  valueAt,
  type SettingsLoaded,
  type SettingsResourceOptions,
  type SettingsSaved,
} from './useSettingsResource'

interface Limits {
  task_limit: number
  health: { silent_after: string }
}

/** A promise the test answers. */
interface Held<T> {
  promise: Promise<T>
  resolve(v: T): void
  reject(e: unknown): void
}
function held<T>(): Held<T> {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

/** The store's event side: handlers, snapshots and the connection state. */
function fakeStore() {
  const handlers: ((ev: ApiEvent) => void)[] = []
  const snapshots: (() => void)[] = []
  const connection = ref<StreamState>('live')
  let id = 0
  return {
    store: {
      onEvent(kind: string, fn: (ev: ApiEvent) => void) {
        expect(kind).toBe('*')
        handlers.push(fn)
        return () => handlers.splice(handlers.indexOf(fn), 1)
      },
      onSnapshot(fn: () => void) {
        snapshots.push(fn)
        return () => snapshots.splice(snapshots.indexOf(fn), 1)
      },
      connection,
    },
    connection,
    handlers,
    emit(kind: string, data: unknown, at = '2026-11-15T14:02:00.000000000Z'): ApiEvent {
      const ev: ApiEvent = { id: ++id, kind, ticket: null, actor: { role: 'user' }, at, data }
      for (const h of [...handlers]) h(ev)
      return ev
    },
    setNextId(n: number) {
      id = n - 1
    },
    snapshot() {
      for (const f of [...snapshots]) f()
    },
  }
}

const limits = (n: number, silent = '30m'): Limits => ({ task_limit: n, health: { silent_after: silent } })
const loaded = (v: Limits, revision: string, errors?: FileError[]): SettingsLoaded<Limits> => ({ value: v, revision, errors })
const saved = (v: Limits, revision: string, eventId: number | null = null): SettingsSaved<Limits> => ({ value: v, revision, eventId })

const watchesGeneral = (ev: ApiEvent) =>
  ev.kind === 'settings.invalid' || (ev.kind === 'settings.changed' && (ev.data as { section?: string }).section === 'general')

/** A resource whose reads and saves the test answers one by one. */
function setup(over: Partial<SettingsResourceOptions<Limits>> = {}) {
  const s = fakeStore()
  const loads: Held<SettingsLoaded<Limits>>[] = []
  const saves: { draft: Limits; revision: string; answer: Held<SettingsSaved<Limits>> }[] = []
  const said: { polite: string[]; assertive: string[] } = { polite: [], assertive: [] }
  const r = createSettingsResource<Limits>(
    {
      load: () => {
        const h = held<SettingsLoaded<Limits>>()
        loads.push(h)
        return h.promise
      },
      save: (draft, revision) => {
        const answer = held<SettingsSaved<Limits>>()
        saves.push({ draft, revision, answer })
        return answer.promise
      },
      watches: watchesGeneral,
      ...over,
    },
    { store: s.store, announce: { polite: (t) => said.polite.push(t), assertive: (t) => said.assertive.push(t) } },
  )
  disposers.push(r.dispose)
  return { r, s, loads, saves, said }
}

const disposers: (() => void)[] = []
afterEach(() => {
  for (const d of disposers.splice(0)) d()
})

async function ready(over: Partial<SettingsResourceOptions<Limits>> = {}, first = loaded(limits(4), 'r1')) {
  const t = setup(over)
  expect(t.r.loading.value).toBe(true)
  t.loads[0]!.resolve(first)
  await flushPromises()
  expect(t.r.loading.value).toBe(false)
  return t
}

const changed = (source: 'app' | 'disk' = 'app') => ({ section: 'general', name: '', source })

test('loads the item with its revision; the draft is a copy and dirty follows it', async () => {
  const { r } = await ready()
  expect(r.value.value).toEqual(limits(4))
  expect(r.revision.value).toBe('r1')
  expect(r.dirty.value).toBe(false)
  r.draft.value!.task_limit = 6
  expect(r.dirty.value).toBe(true)
  expect(r.value.value!.task_limit).toBe(4)
  r.draft.value!.task_limit = 4
  expect(r.dirty.value).toBe(false)
})

test('without a draft, a change of the watched item is shown at once; other events are ignored', async () => {
  const { r, s, loads } = await ready()
  s.emit('settings.changed', { section: 'roles', name: 'pm', source: 'app' })
  s.emit('ticket.created', {})
  await flushPromises()
  expect(loads).toHaveLength(1)
  s.emit('settings.changed', changed())
  await flushPromises()
  expect(loads).toHaveLength(2)
  loads[1]!.resolve(loaded(limits(8), 'r2'))
  await flushPromises()
  expect(r.value.value).toEqual(limits(8))
  expect(r.draft.value).toEqual(limits(8))
  expect(r.revision.value).toBe('r2')
  expect(r.conflict.value).toBeNull()
})

test('a draft survives an external change; Use the new value replaces it', async () => {
  const { r, s, loads, said } = await ready()
  r.draft.value!.task_limit = 6
  s.emit('settings.changed', changed())
  await flushPromises()
  loads[1]!.resolve(loaded(limits(8), 'r2'))
  await flushPromises()
  expect(r.draft.value!.task_limit).toBe(6)
  expect(r.conflict.value).toEqual(loaded(limits(8), 'r2'))
  expect(r.status.value).toBe('conflict')
  // Still built on what the draft started from until the user chooses.
  expect(r.revision.value).toBe('r1')
  expect(said.polite).toContain('This changed outside this page (on disk or in another tab).')

  r.useTheirs()
  expect(r.draft.value).toEqual(limits(8))
  expect(r.value.value).toEqual(limits(8))
  expect(r.revision.value).toBe('r2')
  expect(r.dirty.value).toBe(false)
  expect(r.conflict.value).toBeNull()
})

test('Keep mine keeps the draft and saves it with the new revision', async () => {
  const { r, s, loads, saves } = await ready()
  r.draft.value!.task_limit = 6
  s.emit('settings.changed', changed('disk'))
  await flushPromises()
  loads[1]!.resolve(loaded(limits(8), 'r2'))
  await flushPromises()

  // Save waits for the choice.
  expect(await r.save()).toBe('conflict')
  expect(saves).toHaveLength(0)
  expect(r.error.value).toBe(CHOOSE_FIRST_TEXT)

  r.keepMine()
  expect(r.draft.value!.task_limit).toBe(6)
  expect(r.value.value).toEqual(limits(8))
  expect(r.revision.value).toBe('r2')
  expect(r.dirty.value).toBe(true)
  expect(r.error.value).toBe('')

  const done = r.save()
  await flushPromises()
  expect(saves[0]!.revision).toBe('r2')
  expect(saves[0]!.draft.task_limit).toBe(6)
  saves[0]!.answer.resolve(saved(limits(6), 'r3', 40))
  expect(await done).toBe('saved')
  expect(r.dirty.value).toBe(false)
  expect(r.status.value).toBe('saved')
})

test('an external change that returns to the stored value ends the conflict; one equal to the draft needs no choice', async () => {
  const { r, s, loads } = await ready()
  r.draft.value!.task_limit = 6
  s.emit('settings.changed', changed())
  await flushPromises()
  loads[1]!.resolve(loaded(limits(8), 'r2'))
  await flushPromises()
  expect(r.conflict.value).not.toBeNull()
  s.emit('settings.changed', changed())
  await flushPromises()
  loads[2]!.resolve(loaded(limits(4), 'r3'))
  await flushPromises()
  expect(r.conflict.value).toBeNull()
  expect(r.revision.value).toBe('r3')
  expect(r.draft.value!.task_limit).toBe(6)

  // Another tab stored exactly what the draft says.
  s.emit('settings.changed', changed())
  await flushPromises()
  loads[3]!.resolve(loaded(limits(6), 'r4'))
  await flushPromises()
  expect(r.conflict.value).toBeNull()
  expect(r.dirty.value).toBe(false)
  expect(r.revision.value).toBe('r4')
})

test('stale_settings with untouched keys resends once with the new revision', async () => {
  const { r, loads, saves } = await ready({ keys: ['task_limit'] })
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  expect(saves[0]!.revision).toBe('r1')
  saves[0]!.answer.reject(new ApiError(409, 'stale_settings', 'settings.yaml changed since you loaded it.'))
  await flushPromises()
  // Read again: someone else changed another key only.
  loads[1]!.resolve(loaded(limits(4, '1h'), 'r2'))
  await flushPromises()
  expect(saves).toHaveLength(2)
  expect(saves[1]!.revision).toBe('r2')
  expect(saves[1]!.draft.task_limit).toBe(6)
  saves[1]!.answer.resolve(saved(limits(6, '1h'), 'r3'))
  expect(await done).toBe('saved')
  expect(r.value.value).toEqual(limits(6, '1h'))
  expect(r.draft.value).toEqual(limits(6, '1h'))
  expect(r.conflict.value).toBeNull()
})

test('stale_settings with touched keys shows the conflict instead of resending', async () => {
  const { r, loads, saves } = await ready({ keys: (draft, base) => (draft.task_limit !== base.task_limit ? ['task_limit'] : []) })
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  saves[0]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  loads[1]!.resolve(loaded(limits(9), 'r2'))
  expect(await done).toBe('conflict')
  expect(saves).toHaveLength(1)
  expect(r.conflict.value).toEqual(loaded(limits(9), 'r2'))
  expect(r.draft.value!.task_limit).toBe(6)
  expect(r.revision.value).toBe('r1')
})

test('stale_settings with untouched keys over a file that broke meanwhile asks before resending', async () => {
  const asked: (readonly FileError[])[] = []
  let agree = false
  const { r, loads, saves } = await ready({
    keys: ['task_limit'],
    confirmBroken: (errors) => {
      asked.push(errors)
      return Promise.resolve(agree)
    },
  })
  const bad = [{ file: 'settings.yaml', message: 'line 3: bad' }]
  r.draft.value!.task_limit = 6
  const first = r.save()
  await flushPromises()
  saves[0]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  loads[1]!.resolve(loaded(limits(4), 'r2', bad))
  expect(await first).toBe('cancelled')
  expect(asked).toEqual([bad])
  expect(saves).toHaveLength(1)
  expect(r.broken.value).toEqual(bad)
  expect(r.draft.value!.task_limit).toBe(6)
  expect(r.conflict.value).toBeNull()

  // Asked again (the page shows the broken file now), agreed: sent with the new revision.
  agree = true
  const second = r.save()
  await flushPromises()
  expect(asked).toHaveLength(2)
  saves[1]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  loads[2]!.resolve(loaded(limits(4), 'r3', bad))
  await flushPromises()
  // The same broken file the user just agreed to save over: no second question.
  expect(asked).toHaveLength(2)
  expect(saves).toHaveLength(3)
  expect(saves[2]!.revision).toBe('r3')
  saves[2]!.answer.resolve(saved(limits(6), 'r4'))
  expect(await second).toBe('saved')
})

test('stale_settings without keys over a file that broke meanwhile asks too', async () => {
  const asked: (readonly FileError[])[] = []
  const { r, loads, saves } = await ready({
    confirmBroken: (errors) => {
      asked.push(errors)
      return Promise.resolve(false)
    },
  })
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  saves[0]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  loads[1]!.resolve(loaded(limits(4), 'r2', [{ file: 'settings.yaml', message: 'line 1: bad' }]))
  expect(await done).toBe('cancelled')
  expect(asked).toHaveLength(1)
  expect(saves).toHaveLength(1)
})

test('stale_settings without keys: a value equal to the base is resent, one equal to what was sent settles', async () => {
  const same = await ready()
  same.r.draft.value!.task_limit = 6
  const a = same.r.save()
  await flushPromises()
  same.saves[0]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  // Key order differs: the same equality as a read.
  same.loads[1]!.resolve(loaded({ health: { silent_after: '30m' }, task_limit: 4 }, 'r2'))
  await flushPromises()
  expect(same.saves).toHaveLength(2)
  expect(same.saves[1]!.revision).toBe('r2')
  same.saves[1]!.answer.resolve(saved(limits(6), 'r3'))
  expect(await a).toBe('saved')
  expect(same.r.conflict.value).toBeNull()

  const sent = await ready()
  sent.r.draft.value!.task_limit = 6
  const b = sent.r.save()
  await flushPromises()
  sent.saves[0]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  sent.loads[1]!.resolve(loaded(limits(6), 'r2'))
  expect(await b).toBe('saved')
  expect(sent.saves).toHaveLength(1)
  expect(sent.r.conflict.value).toBeNull()
  expect(sent.r.revision.value).toBe('r2')
  expect(sent.r.dirty.value).toBe(false)
})

test('stale_settings without keys shows the conflict for a different value; a second stale answer too', async () => {
  const noKeys = await ready()
  noKeys.r.draft.value!.task_limit = 6
  const a = noKeys.r.save()
  await flushPromises()
  noKeys.saves[0]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  noKeys.loads[1]!.resolve(loaded(limits(9), 'r2'))
  expect(await a).toBe('conflict')
  expect(noKeys.saves).toHaveLength(1)
  expect(noKeys.r.conflict.value?.revision).toBe('r2')

  const twice = await ready({ keys: ['task_limit'] })
  twice.r.draft.value!.task_limit = 6
  const b = twice.r.save()
  await flushPromises()
  twice.saves[0]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  twice.loads[1]!.resolve(loaded(limits(4), 'r2'))
  await flushPromises()
  twice.saves[1]!.answer.reject(new ApiError(409, 'stale_settings', 'changed'))
  await flushPromises()
  twice.loads[2]!.resolve(loaded(limits(4, '2h'), 'r3'))
  expect(await b).toBe('conflict')
  expect(twice.saves).toHaveLength(2)
  expect(twice.r.conflict.value?.revision).toBe('r3')
})

test('a field error lands under its field, verbatim, and is announced; an error without a field goes to the top', async () => {
  const { r, saves, said } = await ready()
  r.draft.value!.health.silent_after = '5s'
  const done = r.save()
  await flushPromises()
  saves[0]!.answer.reject(new ApiError(422, 'validation', 'health.silent_after must be at least 1m', { field: 'health.silent_after' }))
  expect(await done).toBe('refused')
  expect(r.fieldErrors.value).toEqual({ 'health.silent_after': 'health.silent_after must be at least 1m' })
  expect(r.error.value).toBe('')
  expect(said.assertive).toEqual(['health.silent_after must be at least 1m'])
  expect(r.draft.value!.health.silent_after).toBe('5s')

  const again = r.save()
  await flushPromises()
  expect(r.fieldErrors.value).toEqual({})
  saves[1]!.answer.reject(new ApiError(422, 'invalid_settings', 'The settings file is not valid.'))
  expect(await again).toBe('refused')
  expect(r.error.value).toBe('The settings file is not valid.')
  r.discard()
  expect(r.error.value).toBe('')
  expect(r.draft.value).toEqual(limits(4))
})

test('a lost answer says so and re-reads; a save that went through ends clean', async () => {
  const { r, loads, saves, said } = await ready()
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  saves[0]!.answer.reject(new ApiError(0, 'network', 'Failed to fetch'))
  expect(await done).toBe('lost')
  expect(r.error.value).toBe(LOST_TEXT)
  expect(said.polite).toContain(LOST_TEXT)
  await flushPromises()
  expect(loads).toHaveLength(2)
  loads[1]!.resolve(loaded(limits(6), 'r2'))
  await flushPromises()
  expect(r.dirty.value).toBe(false)
  expect(r.revision.value).toBe('r2')
  expect(r.conflict.value).toBeNull()
})

test('a lost answer for a save that did not happen keeps the draft', async () => {
  const { r, loads, saves } = await ready()
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  saves[0]!.answer.reject(new ApiError(0, 'network', 'Failed to fetch'))
  await done
  await flushPromises()
  loads[1]!.resolve(loaded(limits(4), 'r1'))
  await flushPromises()
  expect(r.dirty.value).toBe(true)
  expect(r.draft.value!.task_limit).toBe(6)
  expect(r.conflict.value).toBeNull()
})

test('reads answering out of order: an older answer never replaces a newer one', async () => {
  const { r, s, loads } = await ready()
  s.emit('settings.changed', changed())
  await flushPromises()
  s.emit('settings.changed', changed())
  s.emit('settings.changed', changed())
  await flushPromises()
  // One read runs; the asks meanwhile become one more read after it.
  expect(loads).toHaveLength(2)
  loads[1]!.resolve(loaded(limits(5), 'r2'))
  await flushPromises()
  expect(loads).toHaveLength(3)
  loads[2]!.resolve(loaded(limits(7), 'r3'))
  await flushPromises()
  expect(r.value.value).toEqual(limits(7))
  expect(r.revision.value).toBe('r3')
})

test('a read under way when a save starts is dropped; reads asked during the save run after it', async () => {
  const { r, s, loads, saves } = await ready()
  s.emit('settings.changed', changed())
  await flushPromises()
  expect(loads).toHaveLength(2)
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  // The old read answers with what was stored before the save: dropped.
  loads[1]!.resolve(loaded(limits(4), 'r1'))
  await flushPromises()
  // Our own event arrives before the answer: no read now, and no false conflict.
  s.emit('settings.changed', changed())
  await flushPromises()
  expect(loads).toHaveLength(2)
  saves[0]!.answer.resolve(saved(limits(6), 'r2', 99))
  expect(await done).toBe('saved')
  expect(r.value.value).toEqual(limits(6))
  expect(r.conflict.value).toBeNull()
  await flushPromises()
  expect(loads).toHaveLength(3)
  loads[2]!.resolve(loaded(limits(6), 'r2'))
  await flushPromises()
  expect(r.revision.value).toBe('r2')
  expect(r.dirty.value).toBe(false)
})

test('edits made while a save runs stay in the draft', async () => {
  const { r, saves } = await ready()
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  r.draft.value!.health.silent_after = '1h'
  saves[0]!.answer.resolve(saved(limits(6), 'r2'))
  await done
  expect(r.draft.value).toEqual(limits(6, '1h'))
  expect(r.dirty.value).toBe(true)
})

test('events up to the saved event id cause no read; later ones do', async () => {
  const { r, s, loads, saves } = await ready()
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  saves[0]!.answer.resolve(saved(limits(6), 'r2', 10))
  await done
  s.setNextId(10)
  s.emit('settings.changed', changed())
  await flushPromises()
  expect(loads).toHaveLength(1)
  s.emit('settings.changed', changed())
  await flushPromises()
  expect(loads).toHaveLength(2)
})

test('a disk change of the watched item sets changedOnDisk with its time; an app change does not', async () => {
  const { r, s, loads } = await ready()
  s.emit('settings.changed', changed('app'))
  await flushPromises()
  expect(r.changedOnDisk.value).toBeNull()
  loads[1]!.resolve(loaded(limits(4), 'r1'))
  s.emit('settings.changed', { section: 'roles', name: 'pm', source: 'disk' })
  expect(r.changedOnDisk.value).toBeNull()
  s.emit('settings.changed', changed('disk'), '2026-11-15T14:02:07.000000000Z')
  expect(r.changedOnDisk.value).toBe('2026-11-15T14:02:07.000000000Z')
  expect(diskChangeText('2026-11-15T14:02:07.000000000Z')).toMatch(/^Changed on disk at .*\d{2}.*, not through omo\.$/)
})

test('broken files come from the read and from settings.invalid; saving over them asks first', async () => {
  const asked: (readonly FileError[])[] = []
  let agree = false
  const { r, s, loads, saves } = await ready(
    {
      confirmBroken: (errors) => {
        asked.push(errors)
        return Promise.resolve(agree)
      },
    },
    loaded(limits(4), 'r1', [{ file: 'settings.yaml', message: 'line 3: bad' }]),
  )
  expect(r.broken.value).toEqual([{ file: 'settings.yaml', message: 'line 3: bad' }])
  s.emit('settings.invalid', { file: 'roles/pm.yaml', message: 'nope' })
  await flushPromises()
  loads[1]!.resolve(loaded(limits(4), 'r1', [{ file: 'settings.yaml', message: 'line 3: bad' }]))
  await flushPromises()
  expect(r.broken.value.map((e) => e.file)).toEqual(['settings.yaml', 'roles/pm.yaml'])

  r.draft.value!.task_limit = 6
  expect(await r.save()).toBe('cancelled')
  expect(saves).toHaveLength(0)
  expect(asked).toHaveLength(1)
  agree = true
  const done = r.save()
  await flushPromises()
  expect(saves).toHaveLength(1)
  saves[0]!.answer.resolve(saved(limits(6), 'r2'))
  expect(await done).toBe('saved')
  expect(r.broken.value.map((e) => e.file)).toEqual(['settings.yaml'])
  expect(BROKEN_QUESTION.text).toBe('Saving replaces the file on disk, including the edit that cannot be read.')
})

test('a file that becomes valid again clears its settings.invalid', async () => {
  const { r, s } = await ready()
  s.emit('settings.invalid', { file: 'settings.yaml', message: 'bad' })
  expect(r.broken.value).toHaveLength(1)
  s.emit('settings.changed', changed('disk'))
  expect(r.broken.value).toEqual([])
})

test('reads again after a snapshot and when the stream is live again', async () => {
  const { s, loads } = await ready()
  s.snapshot()
  await flushPromises()
  expect(loads).toHaveLength(2)
  loads[1]!.resolve(loaded(limits(4), 'r1'))
  await flushPromises()
  s.connection.value = 'retrying'
  await flushPromises()
  expect(loads).toHaveLength(2)
  s.connection.value = 'live'
  await flushPromises()
  expect(loads).toHaveLength(3)
})

test('a failed first read shows the server message; a later read clears it', async () => {
  const t = setup()
  t.loads[0]!.reject(new ApiError(500, 'internal', 'boom'))
  await flushPromises()
  expect(t.r.loading.value).toBe(false)
  expect(t.r.error.value).toBe('boom')
  expect(t.r.value.value).toBeNull()
  expect(await t.r.save()).toBe('unchanged')
  void t.r.reload()
  t.loads[1]!.resolve(loaded(limits(2), 'r1'))
  await flushPromises()
  expect(t.r.error.value).toBe('')
  expect(t.r.value.value).toEqual(limits(2))
})

test('after dispose nothing is read or applied', async () => {
  const { r, s, loads } = await ready()
  s.emit('settings.changed', changed())
  r.dispose()
  loads[1]!.resolve(loaded(limits(9), 'r9'))
  await flushPromises()
  expect(r.value.value).toEqual(limits(4))
  expect(s.handlers).toHaveLength(0)
})

test('helpers: deep equality ignores key order; valueAt follows dotted paths', () => {
  expect(sameJSON({ a: 1, b: [1, { c: 2 }] }, { b: [1, { c: 2 }], a: 1 })).toBe(true)
  expect(sameJSON({ a: 1 }, { a: 1, b: 2 })).toBe(false)
  expect(sameJSON([1], { 0: 1 })).toBe(false)
  expect(valueAt({ a: { b: [5, 6] } }, 'a.b.1')).toBe(6)
  expect(valueAt({ a: 1 }, 'a.b')).toBeUndefined()
})

test('a read that started before a save and answers after it does not bring back the old value', async () => {
  const { r, s, loads, saves } = await ready()
  s.emit('settings.changed', changed())
  await flushPromises()
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  saves[0]!.answer.resolve(saved(limits(6), 'r2'))
  expect(await done).toBe('saved')
  loads[1]!.resolve(loaded(limits(4), 'r1'))
  await flushPromises()
  expect(r.value.value).toEqual(limits(6))
  expect(r.draft.value).toEqual(limits(6))
  expect(r.revision.value).toBe('r2')
})

interface Rows {
  name: string
  rows: { path: string; ignore: string[] }[]
}

/** A resource over rows whose first save is refused with `field`. */
async function refusedRows(field: string) {
  const s = fakeStore()
  const start: Rows = { name: 'x', rows: [{ path: '/a', ignore: ['p', 'q'] }, { path: '/b', ignore: [] }, { path: '/c', ignore: [] }] }
  const r = createSettingsResource<Rows>(
    {
      load: () => Promise.resolve({ value: JSON.parse(JSON.stringify(start)) as Rows, revision: 'r1' }),
      save: () => Promise.reject(new ApiError(422, 'validation', 'refused here', { field })),
      watches: () => false,
    },
    { store: s.store },
  )
  disposers.push(r.dispose)
  await flushPromises()
  r.draft.value!.name = 'y'
  expect(await r.save()).toBe('refused')
  expect(r.fieldErrors.value).toEqual({ [field]: 'refused here' })
  return r
}

test('a refusal naming a row by index stays while rows are edited in place', async () => {
  const r = await refusedRows('rows.1.path')
  r.draft.value!.rows[1]!.path = '/b2'
  r.draft.value!.rows[0]!.ignore[0] = 'p2'
  expect(r.fieldErrors.value).toEqual({ 'rows.1.path': 'refused here' })
})

test('a refusal naming a row by index is dropped when a row is added, removed or moved', async () => {
  const added = await refusedRows('rows.1.path')
  added.draft.value!.rows.push({ path: '/d', ignore: [] })
  expect(added.fieldErrors.value).toEqual({})

  const removed = await refusedRows('rows.1.path')
  removed.draft.value!.rows.splice(0, 1)
  expect(removed.fieldErrors.value).toEqual({})

  const moved = await refusedRows('rows.1.path')
  const [row] = moved.draft.value!.rows.splice(2, 1)
  moved.draft.value!.rows.splice(1, 0, row!)
  expect(moved.fieldErrors.value).toEqual({})

  // Two rows swapped in one edit (a list replaced by its reordered copy), then one edited: still dropped.
  const swapped = await refusedRows('rows.1.path')
  const list = swapped.draft.value!.rows
  swapped.draft.value!.rows = [list[1]!, list[0]!, list[2]!]
  swapped.draft.value!.rows[0]!.path = '/b2'
  expect(swapped.fieldErrors.value).toEqual({})

  // A nested list: a pattern removed moves the others up.
  const nested = await refusedRows('rows.0.ignore.1')
  nested.draft.value!.rows[0]!.ignore.splice(0, 1)
  expect(nested.fieldErrors.value).toEqual({})
})

test('a refusal naming no index stays through structural edits', async () => {
  const r = await refusedRows('name')
  r.draft.value!.rows.reverse()
  expect(r.fieldErrors.value).toEqual({ name: 'refused here' })
})

test('a field holding no number blocks Save, counts as an edit, and Discard clears it', async () => {
  const { r, saves, said } = await ready()
  expect(r.blocked.value).toBe(false)
  const resets = r.resets.value
  r.setParse('max', 'Enter a whole number')
  expect(r.blocked.value).toBe(true)
  // Only text that is no number: Discard and the leave question must still see an edit.
  expect(r.dirty.value).toBe(true)
  expect(await r.save()).toBe('refused')
  expect(saves).toHaveLength(0)
  expect(r.error.value).toBe(BLOCKED_TEXT)
  expect(said.assertive).toEqual([BLOCKED_TEXT])
  r.discard()
  expect(r.resets.value).toBe(resets + 1)
  expect(r.blocked.value).toBe(false)
  expect(r.dirty.value).toBe(false)
  expect(r.error.value).toBe('')

  r.setParse('max', 'Enter a whole number')
  r.setParse('max', null)
  expect(r.blocked.value).toBe(false)
  expect(r.dirty.value).toBe(false)
})

test('a read that replaces a clean form resets its fields; a held conflict taken resets them too', async () => {
  const { r, s, loads } = await ready()
  const before = r.resets.value
  s.emit('settings.changed', changed('disk'))
  await flushPromises()
  loads[1]!.resolve(loaded(limits(5), 'r2'))
  await flushPromises()
  expect(r.resets.value).toBe(before + 1)

  r.setParse('max', 'Enter a whole number')
  s.emit('settings.changed', changed('disk'))
  await flushPromises()
  loads[2]!.resolve(loaded(limits(7), 'r3'))
  await flushPromises()
  // The text the user is typing is an edit: the new value is held as a conflict.
  expect(r.conflict.value?.revision).toBe('r3')
  r.useTheirs()
  expect(r.resets.value).toBe(before + 2)
  expect(r.blocked.value).toBe(false)
  expect(r.draft.value!.task_limit).toBe(7)
})

test('an item that is not stored yet counts as unsaved until it is saved, also after Discard', async () => {
  const { r, saves } = await ready({}, { value: limits(4), revision: '0', unsaved: true })
  expect(r.dirty.value).toBe(true)
  r.discard()
  expect(r.dirty.value).toBe(true)
  const done = r.save()
  await flushPromises()
  expect(saves[0]!.revision).toBe('0')
  saves[0]!.answer.resolve(saved(limits(4), 'r1'))
  expect(await done).toBe('saved')
  expect(r.dirty.value).toBe(false)
})

test('a save refused for want of a login keeps the draft, says so, and the note goes with the next read', async () => {
  const { r, s, loads, saves } = await ready()
  r.draft.value!.task_limit = 6
  const done = r.save()
  await flushPromises()
  saves[0]!.answer.reject(new ApiError(401, 'unauthorized', 'missing or invalid token'))
  expect(await done).toBe('refused')
  expect(r.error.value).toBe(LOCKED_TEXT)
  expect(r.fieldErrors.value).toEqual({})
  expect(r.draft.value!.task_limit).toBe(6)
  // Unlocked: the board's snapshot reads the item again, the draft stays and saves.
  s.snapshot()
  await flushPromises()
  loads[1]!.resolve(loaded(limits(4), 'r1'))
  await flushPromises()
  expect(r.error.value).toBe('')
  expect(r.draft.value!.task_limit).toBe(6)
  const again = r.save()
  await flushPromises()
  saves[1]!.answer.resolve(saved(limits(6), 'r2'))
  expect(await again).toBe('saved')
})
