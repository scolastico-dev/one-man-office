import { flushPromises } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ref } from 'vue'
import { ApiError, type ApiClient } from '../../../api/client'
import type { ApiEvent, General, SettingsResponse } from '../../../api/types'
import { general } from '../../../testing/fakeOmo'
import { BLOCKED_TEXT, createGeneralForm, numberFieldOf, pickGeneral, unplacedErrors, watchesGeneral, type GeneralForm } from './generalForms'

interface Sent {
  method: string
  path: string
  body: unknown
  ifMatch?: string
}

/** A client whose file is `stored` at `revision`; a PATCH is refused as stale when its If-Match is not the revision. */
function fakeClient() {
  const file = {
    settings: structuredClone(general) as General,
    revision: 'r1',
  }
  const sent: Sent[] = []
  let failNext: ApiError | null = null
  const client = {
    async get(path: string) {
      expect(path).toBe('/api/settings')
      const data: SettingsResponse = {
        settings: structuredClone(file.settings),
        errors: [],
        revision: file.revision,
        home: '/home/x/omo',
      }
      return { data, eventId: null }
    },
    async send(method: string, path: string, body: unknown, opts?: { ifMatch?: string }) {
      sent.push({ method, path, body, ifMatch: opts?.ifMatch })
      if (failNext) {
        const e = failNext
        failNext = null
        throw e
      }
      if (opts?.ifMatch !== file.revision)
        throw new ApiError(409, 'stale_settings', 'settings.yaml changed since you loaded it. Load it again to see the change.')
      Object.assign(file.settings, structuredClone(body as Partial<General>))
      file.revision = `r${Number(file.revision.slice(1)) + 1}`
      const data: SettingsResponse = {
        settings: structuredClone(file.settings),
        errors: [],
        revision: file.revision,
        home: '/home/x/omo',
      }
      return { data, eventId: 7 }
    },
  } as unknown as ApiClient
  return {
    client,
    sent,
    file,
    /** A hand edit. */
    edit(change: (g: General) => void) {
      change(file.settings)
      file.revision = `r${Number(file.revision.slice(1)) + 1}`
    },
    failNextWith(e: ApiError) {
      failNext = e
    },
  }
}

const forms: GeneralForm<keyof General>[] = []
afterEach(() => {
  for (const f of forms.splice(0)) f.dispose()
})

async function limits(f = fakeClient()) {
  const said: string[] = []
  const store = {
    onEvent: () => () => {},
    onSnapshot: () => () => {},
    connection: ref('live' as const),
  }
  const form = createGeneralForm(['max_running_agents', 'health'], f.client, {
    store,
    announce: { polite: () => {}, assertive: (t) => said.push(t) },
  })
  forms.push(form as GeneralForm<keyof General>)
  await flushPromises()
  return { form, f, said }
}

test('a form holds only its own keys, and the home folder', async () => {
  const { form } = await limits()
  expect(Object.keys(form.draft.value!).sort()).toEqual(['health', 'max_running_agents'])
  expect(form.home.value).toBe('/home/x/omo')
  expect(pickGeneral(general, ['ceo'])).toEqual({ ceo: general.ceo })
})

test('each page sends only its own keys, with the revision it read', async () => {
  const { form, f } = await limits()
  form.draft.value!.max_running_agents = 2
  expect(await form.save()).toBe('saved')
  expect(f.sent).toEqual([
    {
      method: 'PATCH',
      path: '/api/settings',
      body: { max_running_agents: 2, health: general.health },
      ifMatch: 'r1',
    },
  ])
  expect(f.file.settings.review_defaults).toEqual(general.review_defaults)
  expect(form.revision.value).toBe('r2')
})

test('a change of other keys is no conflict: the save is sent again with the new revision', async () => {
  const { form, f } = await limits()
  form.draft.value!.max_running_agents = 2
  f.edit((g) => (g.review_defaults.story = 'skipped'))
  expect(await form.save()).toBe('saved')
  expect(f.sent.map((s) => s.ifMatch)).toEqual(['r1', 'r2'])
  expect(f.file.settings.max_running_agents).toBe(2)
  // The other page's key survived the write.
  expect(f.file.settings.review_defaults.story).toBe('skipped')
  expect(form.conflict.value).toBeNull()
})

test('a change of its own key under an edit is a conflict; Keep mine then saves', async () => {
  const { form, f } = await limits()
  form.draft.value!.max_running_agents = 2
  f.edit((g) => (g.max_running_agents = 9))
  expect(await form.save()).toBe('conflict')
  expect(form.conflict.value?.value.max_running_agents).toBe(9)
  form.keepMine()
  expect(await form.save()).toBe('saved')
  expect(f.file.settings.max_running_agents).toBe(2)
})

test('a refusal names its field', async () => {
  const { form, f, said } = await limits()
  form.draft.value!.health.silent_after = '1s'
  f.failNextWith(new ApiError(422, 'invalid_settings', 'health.silent_after must be at least 1m', { field: 'health.silent_after' }))
  expect(await form.save()).toBe('refused')
  expect(form.fieldErrors.value).toEqual({
    'health.silent_after': 'health.silent_after must be at least 1m',
  })
  expect(said).toEqual(['health.silent_after must be at least 1m'])
})

test('Save is blocked while a field holds no number, then free again', async () => {
  const { form, f, said } = await limits()
  const n = numberFieldOf(
    form,
    'max_running_agents',
    () => form.draft.value!.max_running_agents,
    (v) => (form.draft.value!.max_running_agents = v),
  )
  form.draft.value!.health.silent_after = '20m'
  n.onParse('Enter a whole number')
  expect(await form.save()).toBe('refused')
  expect(f.sent).toEqual([])
  expect(said).toEqual([BLOCKED_TEXT])
  expect(form.error.value).toBe(BLOCKED_TEXT)
  expect(n.error.value).toBe('Enter a whole number')
  // An empty field is no number either; a number clears it.
  n.model.value = null
  expect(n.error.value).toBe('Enter a whole number')
  n.model.value = 3
  expect(n.error.value).toBeUndefined()
  expect(form.error.value).toBe('')
  expect(await form.save()).toBe('saved')
  expect(f.file.settings.max_running_agents).toBe(3)
})

test('only changes of the general section and a broken settings.yaml are watched', () => {
  const ev = (kind: string, data: unknown) =>
    ({
      id: 1,
      kind,
      ticket: null,
      actor: { role: 'system' },
      at: '',
      data,
    }) as ApiEvent
  expect(watchesGeneral(ev('settings.changed', { section: 'general', name: '', source: 'disk' }))).toBe(true)
  expect(watchesGeneral(ev('settings.changed', { section: 'roles', name: 'pm', source: 'app' }))).toBe(false)
  expect(
    watchesGeneral(
      ev('settings.invalid', {
        file: '/home/x/omo/settings.yaml',
        message: 'line 3: bad',
      }),
    ),
  ).toBe(true)
  expect(
    watchesGeneral(
      ev('settings.invalid', {
        file: '/home/x/omo/roles/pm.yaml',
        message: 'bad',
      }),
    ),
  ).toBe(false)
  expect(watchesGeneral(ev('ticket.updated', {}))).toBe(false)
})

test('a refusal that names a group the page has no field for is shown apart', async () => {
  const { form, f } = await limits()
  form.draft.value!.health.silent_after = 'soon'
  f.failNextWith(new ApiError(400, 'bad_request', 'invalid request body: a value of health is not valid', { field: 'health' }))
  expect(await form.save()).toBe('refused')
  expect(unplacedErrors(form, ['health.silent_after', 'max_running_agents'])).toEqual(['invalid request body: a value of health is not valid'])
  expect(unplacedErrors(form, ['health'])).toEqual([])
})
