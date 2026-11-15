import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../../api/client'
import { HIDDEN_VALUE, type Profile } from '../../../api/types'
import { mountIn, setup } from './agentsTesting'
import FolderPickerDialog from '../projects/FolderPickerDialog.vue'
import ProfileEditor from './ProfileEditor.vue'

const claude = (): Profile => ({
  name: 'claude',
  cli: 'claude',
  command: 'claude',
  args: ['--verbose'],
  env: { ANTHROPIC_API_KEY: HIDDEN_VALUE, HTTPS_PROXY: '$OMO_PROXY' },
  account_dir: '',
  usage_script: 'claude-usage',
  prompt_delivery: {},
  models: {
    sonnet: { args: ['--model', 'sonnet'], env: { FOO: HIDDEN_VALUE }, usage_windows: ['5h'], description: 'Everyday.' },
  },
  revision: '0123456789abcdef',
})

/** The Save button of the Save bar, whatever its markup. */
function saveButton(w: VueWrapper) {
  const b = w.findAll('button').find((x) => x.text() === 'Save')
  if (!b) throw new Error('no Save button')
  return b
}

test('editor: a refused save puts the message under the field named in details.field, e.g. models.sonnet.env.FOO', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = () => {
    throw new ApiError(422, 'validation', 'Enter the value of FOO again.', { field: 'models.sonnet.env.FOO' })
  }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-command]').setValue('claude2')
  await saveButton(w).trigger('click')
  await flushPromises()

  expect(s.sent).toHaveLength(1)
  expect(s.sent[0]!.ifMatch).toBe('0123456789abcdef')
  const row = w.get('[data-model-editor="sonnet"] [data-env-row="FOO"]')
  expect(row.text()).toContain('Enter the value of FOO again.')
  expect(w.find('[data-unplaced-errors]').exists()).toBe(false)
})

test('editor: a refusal naming a field the form does not show is shown at the top', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = () => {
    throw new ApiError(422, 'validation', 'usage_script must be the name of a script', { field: 'usage_script' })
  }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-command]').setValue('claude2')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect(w.get('[data-unplaced-errors] .callout__body').text()).toBe('usage_script must be the name of a script')
})

test('editor: a new profile saves with If-Match "0"; a name taken meanwhile shows the conflict', async () => {
  const s = await setup()
  const w = await mountIn(s, ProfileEditor, { name: 'work', newCli: 'codex' })
  expect(w.text()).toContain('(not saved yet)')
  expect((w.get('[data-command]').element as HTMLInputElement).value).toBe('codex')
  await w.get('[data-command]').setValue('/opt/codex')

  // Someone else creates "work" before the save.
  s.state.profiles = { profiles: [{ ...claude(), name: 'work', revision: 'ffffffffffffffff' }], errors: [] }
  s.state.write = () => {
    throw new ApiError(409, 'stale_settings', 'profiles/work.yaml changed since you loaded it. Load it again to see the change.')
  }
  await saveButton(w).trigger('click')
  await flushPromises()

  expect(s.sent).toHaveLength(1)
  expect(s.sent[0]!.method).toBe('PUT')
  expect(s.sent[0]!.path).toBe('/api/profiles/work')
  expect(s.sent[0]!.ifMatch).toBe('0')
  expect((s.sent[0]!.body as Profile).command).toBe('/opt/codex')
  expect(Object.keys((s.sent[0]!.body as Profile).models!)).toEqual(['default'])
  expect(w.find('[data-use-theirs]').exists()).toBe(true)
  expect(w.find('[data-keep-mine]').exists()).toBe(true)
})

test('a hidden value can be replaced or removed, never shown', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = (x) => ({ ...(x.body as Profile), revision: '1111111111111111', env: { ANTHROPIC_API_KEY: HIDDEN_VALUE } })
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })

  // Nowhere in the page: not as text, not as an input's value.
  expect(w.html()).not.toContain(HIDDEN_VALUE)
  const values = w.findAll('input').map((i) => (i.element as HTMLInputElement).value)
  expect(values).not.toContain(HIDDEN_VALUE)
  expect(w.get('[data-env-row="ANTHROPIC_API_KEY"]').text()).toContain('hidden')

  // An unchanged secret goes back as the placeholder, so omo keeps it.
  await w.get('[data-command]').setValue('claude-x')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect((s.sent[0]!.body as Profile).env).toEqual({ ANTHROPIC_API_KEY: HIDDEN_VALUE, HTTPS_PROXY: '$OMO_PROXY' })
  expect((s.sent[0]!.body as Profile).models!.sonnet!.env).toEqual({ FOO: HIDDEN_VALUE })
  expect((s.sent[0]!.body as Profile).usage_script).toBe('claude-usage')
  expect((s.sent[0]!.body as Profile).models!.sonnet!.usage_windows).toEqual(['5h'])

  // Replace: a new value is typed; Escape would bring the stored one back.
  await w.get('[data-env-row="ANTHROPIC_API_KEY"] [data-part="replace"]').trigger('click')
  await w.get('[data-env-row="ANTHROPIC_API_KEY"] [data-part="value"]').setValue('sk-new')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect((s.sent[1]!.body as Profile).env!.ANTHROPIC_API_KEY).toBe('sk-new')

  // The answer hides it again; remove it.
  expect(w.html()).not.toContain('sk-new')
  await w.get('[data-env-row="ANTHROPIC_API_KEY"] [data-part="remove"]').trigger('click')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect((s.sent[2]!.body as Profile).env).toEqual({})
  expect(w.html()).not.toContain(HIDDEN_VALUE)
})

test('editor: models are added and removed; a removal refused by the server is shown verbatim', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = () => {
    throw new ApiError(409, 'in_use', 'Open tickets use the model claude/sonnet: #4, #9.')
  }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-add-model]').trigger('click')
  await w.get('[data-model-name]').setValue('opus')
  await w.get('[data-model-editor="sonnet"] [data-remove-model]').trigger('click')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect(Object.keys((s.sent[0]!.body as Profile).models!)).toEqual(['opus'])
  expect(w.text()).toContain('Open tickets use the model claude/sonnet: #4, #9.')
})

test('editor: deleting a profile in use shows the server message verbatim', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = () => {
    throw new ApiError(409, 'in_use', 'profile claude is used by the roles developer')
  }
  vi.stubGlobal('confirm', () => true)
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-delete-profile]').trigger('click')
  await flushPromises()
  expect(s.sent[0]).toMatchObject({ method: 'DELETE', path: '/api/profiles/claude', ifMatch: '0123456789abcdef' })
  expect(w.get('[data-delete-error] .callout__body').text()).toBe('profile claude is used by the roles developer')
  expect(w.emitted()).not.toHaveProperty('deleted')
})


test('editor: a new profile counts as unsaved at once: Save is enabled and closing the tab asks', async () => {
  const s = await setup()
  const w = await mountIn(s, ProfileEditor, { name: 'work', newCli: 'codex' })
  expect(saveButton(w).attributes('disabled')).toBeUndefined()
  expect(w.text()).toContain('Unsaved changes')
  const editor = w.findComponent(ProfileEditor)
  expect(editor.emitted('dirty')!.at(-1)).toEqual([true])
  const e = new Event('beforeunload', { cancelable: true })
  window.dispatchEvent(e)
  expect(e.defaultPrevented).toBe(true)

  // Saved as it is, without an edit.
  s.state.write = (sent) => ({ ...(sent.body as Profile), revision: 'aaaaaaaaaaaaaaaa' })
  await saveButton(w).trigger('click')
  await flushPromises()
  expect(s.sent).toHaveLength(1)
  expect(s.sent[0]!.ifMatch).toBe('0')
  expect(editor.emitted('dirty')!.at(-1)).toEqual([false])
})

test('editor: a profile created in this visit and then deleted on disk says it no longer exists', async () => {
  const s = await setup()
  const w = await mountIn(s, ProfileEditor, { name: 'work', newCli: 'codex' })
  s.state.write = (sent) => {
    const p = { ...(sent.body as Profile), revision: 'aaaaaaaaaaaaaaaa' }
    s.state.profiles = { profiles: [p], errors: [] }
    return p
  }
  await saveButton(w).trigger('click')
  await flushPromises()
  expect(w.text()).not.toContain('(not saved yet)')

  s.state.profiles = { profiles: [], errors: [] }
  s.omo.emit('settings.changed', { section: 'profiles', name: 'work', source: 'disk' })
  await flushPromises()
  expect(w.get('[data-profile-gone]').text()).toContain('The profile work no longer exists.')
  expect(w.find('[data-command]').exists()).toBe(false)
  expect(w.text()).not.toContain('(not saved yet)')
})

test('editor: Browse… next to the account folder chooses it in the folder picker', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [{ ...claude(), account_dir: '~/.claude-work' }], errors: [] }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  const browse = w.get('[data-account-browse]')
  expect(browse.attributes('aria-label')).toBe('Browse… for the account folder')
  await browse.trigger('click')
  await flushPromises()
  const picker = w.findComponent(FolderPickerDialog)
  expect(picker.props('open')).toBe(true)
  expect(picker.props('start')).toBe('~/.claude-work')
  picker.vm.$emit('choose', '/home/you/.claude-other')
  await flushPromises()
  expect((w.get('input[data-account-dir]').element as HTMLInputElement).value).toBe('/home/you/.claude-other')
  expect(w.text()).toContain('Unsaved changes')
})
