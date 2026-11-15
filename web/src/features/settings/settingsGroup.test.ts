import { flushPromises } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { ref } from 'vue'
import { ApiError } from '../../api/client'
import type { StreamState } from '../../api/eventStream'
import type { ApiEvent } from '../../api/types'
import { createSettingsGroup } from './settingsGroup'
import { CHOOSE_FIRST_TEXT, type SettingsResourceOptions } from './useSettingsResource'

interface Role {
  models: string[]
}

function fakeStore() {
  const handlers: ((ev: ApiEvent) => void)[] = []
  return {
    store: {
      onEvent(_kind: string, fn: (ev: ApiEvent) => void) {
        handlers.push(fn)
        return () => handlers.splice(handlers.indexOf(fn), 1)
      },
      onSnapshot() {
        return () => {}
      },
      connection: ref<StreamState>('live'),
    },
  }
}

interface RoleFile {
  value: Role
  revision: string
  refuse: ApiError | null
  saves: number
  options: SettingsResourceOptions<Role>
}

/** One stored role file: its value and revision, and what its next save answers. */
function file(models: string[]): RoleFile {
  const f: RoleFile = {
    value: { models },
    revision: 'r1',
    refuse: null,
    saves: 0,
    options: {
      load: async () => ({ value: structuredClone(f.value), revision: f.revision }),
      save: async (draft: Role, rev: string) => {
        f.saves++
        if (f.refuse) throw f.refuse
        if (rev !== f.revision) throw new ApiError(409, 'stale_settings', 'changed meanwhile')
        f.value = structuredClone(draft)
        f.revision = `${f.revision}+`
        return { value: structuredClone(f.value), revision: f.revision, eventId: null }
      },
      watches: () => false,
    },
  }
  return f
}

function setup() {
  const said: string[] = []
  const files = { ceo: file(['a']), pm: file(['a']), developer: file(['a']), reviewer: file(['a']) }
  const g = createSettingsGroup<'ceo' | 'pm' | 'developer' | 'reviewer', Role>(
    [
      { key: 'ceo', name: 'the CEO', options: files.ceo.options },
      { key: 'pm', name: 'the product manager', options: files.pm.options },
      { key: 'developer', name: 'the developer', options: files.developer.options },
      { key: 'reviewer', name: 'the reviewer', options: files.reviewer.options },
    ],
    { ...fakeStore(), announce: { polite: (t) => said.push(t), assertive: (t) => said.push(`!${t}`) } },
  )
  return { g, files, said }
}

test('one Save writes every changed member, in order, and says Saved once', async () => {
  const { g, files, said } = setup()
  await flushPromises()
  expect(g.dirty.value).toBe(false)
  g.members.pm.draft.value!.models.push('b')
  g.members.reviewer.draft.value!.models = ['c']
  expect(g.dirty.value).toBe(true)
  expect(await g.save()).toEqual({ pm: 'saved', reviewer: 'saved' })
  expect(files.ceo.saves + files.developer.saves).toBe(0)
  expect(files.pm.value.models).toEqual(['a', 'b'])
  expect(files.reviewer.value.models).toEqual(['c'])
  expect(g.dirty.value).toBe(false)
  expect(g.status.value).toBe('saved')
  expect(g.error.value).toBe('')
  expect(said).toEqual(['Saved.'])
  g.dispose()
})

test('a refusal of one member and a conflict on another: the members saved before and after stay saved, each failure stays with its member', async () => {
  const { g, files, said } = setup()
  await flushPromises()
  for (const k of ['ceo', 'pm', 'developer', 'reviewer'] as const) g.members[k].draft.value!.models.push('x')
  files.pm.refuse = new ApiError(422, 'validation', 'unknown model “x”', { field: 'models.1' })
  // The developer's file changed meanwhile to something else: a conflict, nothing written over it.
  files.developer.value = { models: ['other'] }
  files.developer.revision = 'r2'

  const out = await g.save()
  expect(out).toEqual({ ceo: 'saved', pm: 'refused', developer: 'conflict', reviewer: 'saved' })
  expect(files.ceo.value.models).toEqual(['a', 'x'])
  expect(files.reviewer.value.models).toEqual(['a', 'x'])
  expect(files.developer.value.models).toEqual(['other'])
  // The refusal is under the product manager's field, the conflict waits for a choice on the developer.
  expect(g.members.pm.fieldErrors.value).toEqual({ 'models.1': 'unknown model “x”' })
  expect(g.members.developer.conflict.value?.value).toEqual({ models: ['other'] })
  expect(g.status.value).toBe('conflict')
  expect(g.error.value).toBe('Saved the CEO and the reviewer. Not saved: the product manager and the developer; see the message under each.')
  expect(said).toContain('!unknown model “x”')
  expect(said).not.toContain('Saved.')

  // Saving again while the conflict waits: the developer asks for the choice first, the others are done.
  files.pm.refuse = null
  g.members.pm.draft.value!.models = ['a', 'b']
  expect(await g.save()).toEqual({ pm: 'saved', developer: 'conflict' })
  expect(files.pm.value.models).toEqual(['a', 'b'])
  expect(g.members.developer.error.value).toBe(CHOOSE_FIRST_TEXT)
  expect(g.error.value).toBe('Saved the product manager. Not saved: the developer; see the message under it.')

  // Keep mine: the next Save writes the draft over the new value.
  g.members.developer.keepMine()
  expect(await g.save()).toEqual({ developer: 'saved' })
  expect(files.developer.value.models).toEqual(['a', 'x'])
  expect(g.status.value).toBe('saved')
  expect(g.error.value).toBe('')
  g.dispose()
})

test('Discard drops every draft; in a conflict it takes the new value', async () => {
  const { g, files } = setup()
  await flushPromises()
  g.members.ceo.draft.value!.models = ['z']
  g.members.reviewer.draft.value!.models = ['z']
  files.reviewer.value = { models: ['new'] }
  files.reviewer.revision = 'r9'
  await g.save()
  expect(g.members.reviewer.conflict.value).not.toBeNull()
  g.members.ceo.draft.value!.models = ['y']
  g.discard()
  expect(g.members.ceo.draft.value!.models).toEqual(['z'])
  expect(g.members.reviewer.draft.value!.models).toEqual(['new'])
  expect(g.dirty.value).toBe(false)
  expect(g.status.value).toBe(null)
  g.dispose()
})
