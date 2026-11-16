import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../../api/client'
import type { Profile } from '../../../api/types'
import { mountIn, setup } from './agentsTesting'
import FolderPickerDialog from '../projects/FolderPickerDialog.vue'
import ProfileEditor from './ProfileEditor.vue'

const claude = (): Profile => ({
  name: 'claude',
  cli: 'claude',
  command: 'claude',
  args: ['--verbose'],
  env: { ANTHROPIC_API_KEY: 'sk-ant-1', HTTPS_PROXY: '$OMO_PROXY' },
  account_dir: '',
  usage_script: 'claude-usage',
  prompt_delivery: {},
  models: {
    sonnet: { args: ['--model', 'sonnet'], env: { FOO: 'bar' }, usage_windows: ['5h'], description: 'Everyday.' },
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
    throw new ApiError(422, 'validation', 'the value of FOO in models.sonnet.env is longer than 32 KiB', { field: 'models.sonnet.env.FOO' })
  }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-command]').setValue('claude2')
  await saveButton(w).trigger('click')
  await flushPromises()

  expect(s.sent).toHaveLength(1)
  expect(s.sent[0]!.ifMatch).toBe('0123456789abcdef')
  const row = w.get('[data-model-editor="sonnet"] [data-env-row="FOO"]')
  expect(row.text()).toContain('the value of FOO in models.sonnet.env is longer than 32 KiB')
  expect(w.find('[data-form-errors]').exists()).toBe(false)
})

test('editor: a refusal naming a field the form does not show is shown at the top', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = () => {
    throw new ApiError(422, 'validation', 'future_field is not known', { field: 'future_field' })
  }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-command]').setValue('claude2')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect(w.get('[data-form-errors] .callout__body').text()).toBe('future_field is not known')
})

test('editor: the usage script and the windows are edited, and their refusals land under them', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  const select = w.get<HTMLSelectElement>('[data-usage-script] select, select[data-usage-script]')
  // The stored script is offered even when the list of scripts cannot be read.
  expect(select.element.value).toBe('claude-usage')
  expect([...select.element.options].map((o) => o.value)).toEqual(['', 'claude-usage'])
  await select.setValue('')
  await w.get('[data-model-editor="sonnet"] [data-usage-windows]').findAll('button').find((b) => b.text().includes('Add window'))!.trigger('click')
  const inputs = w.get('[data-model-editor="sonnet"] [data-usage-windows]').findAll('input')
  await inputs[inputs.length - 1]!.setValue('weekly')

  s.state.write = () => {
    throw new ApiError(422, 'validation', 'models.sonnet.usage_windows.1 must be a window name', { field: 'models.sonnet.usage_windows.1' })
  }
  await saveButton(w).trigger('click')
  await flushPromises()
  const body = s.sent[0]!.body as Profile
  expect(body.usage_script).toBe('')
  expect(body.models!.sonnet!.usage_windows).toEqual(['5h', 'weekly'])
  expect(w.get('[data-model-editor="sonnet"] [data-usage-windows]').text()).toContain('models.sonnet.usage_windows.1 must be a window name')
  expect(w.find('[data-form-errors]').exists()).toBe(false)

  s.state.write = () => {
    throw new ApiError(422, 'validation', 'usage_script must name a usage script that exists', { field: 'usage_script' })
  }
  await select.setValue('claude-usage')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect(w.get('[data-usage-script]').element.closest('.omo-field')!.textContent).toContain('usage_script must name a usage script that exists')
  expect(w.find('[data-form-errors]').exists()).toBe(false)
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

test('editor: environment values are shown and edited as typed', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = (x) => ({ ...(x.body as Profile), revision: '1111111111111111' })
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  const value = (sel: string) => (w.get(`${sel} [data-part="value"]`).element as HTMLInputElement).value
  expect(value('[data-env-row="ANTHROPIC_API_KEY"]')).toBe('sk-ant-1')
  expect(value('[data-model-editor="sonnet"] [data-env-row="FOO"]')).toBe('bar')

  // Unchanged values go back as loaded.
  await w.get('[data-command]').setValue('claude-x')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect((s.sent[0]!.body as Profile).env).toEqual({ ANTHROPIC_API_KEY: 'sk-ant-1', HTTPS_PROXY: '$OMO_PROXY' })
  expect((s.sent[0]!.body as Profile).models!.sonnet!.env).toEqual({ FOO: 'bar' })
  expect((s.sent[0]!.body as Profile).usage_script).toBe('claude-usage')
  expect((s.sent[0]!.body as Profile).models!.sonnet!.usage_windows).toEqual(['5h'])

  // A changed value is sent and shown from the answer.
  await w.get('[data-env-row="ANTHROPIC_API_KEY"] [data-part="value"]').setValue('sk-new')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect((s.sent[1]!.body as Profile).env!.ANTHROPIC_API_KEY).toBe('sk-new')
  expect(value('[data-env-row="ANTHROPIC_API_KEY"]')).toBe('sk-new')

  // Removed.
  await w.get('[data-env-row="ANTHROPIC_API_KEY"] [data-part="remove"]').trigger('click')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect((s.sent[2]!.body as Profile).env).toEqual({ HTTPS_PROXY: '$OMO_PROXY' })
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

test('editor: a delete refused for open tickets shows the server message verbatim and offers nothing', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = () => {
    throw new ApiError(409, 'in_use', 'open tickets use the models of profile claude: #4')
  }
  const asked: string[] = []
  vi.stubGlobal('confirm', (q: string) => (asked.push(q), true))
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-delete-profile]').trigger('click')
  await flushPromises()
  expect(s.sent).toHaveLength(1)
  expect(s.sent[0]).toMatchObject({ method: 'DELETE', path: '/api/profiles/claude', ifMatch: '0123456789abcdef' })
  expect(asked).toHaveLength(1)
  expect(w.get('[data-delete-error] .callout__body').text()).toBe('open tickets use the models of profile claude: #4')
  expect(w.emitted()).not.toHaveProperty('deleted')
})

test('editor: a profile that roles use is deleted after a second question naming them, taking its models out of them', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = (sent) => {
    if (!sent.path.endsWith('?detach=true')) throw new ApiError(409, 'in_use', 'profile claude is used by the roles ceo, pm, developer and reviewer', { roles: ['ceo', 'pm', 'developer', 'reviewer'] })
    return { detached: [{ role: 'ceo', models: ['claude/sonnet'] }, { role: 'developer', models: ['claude/sonnet', 'claude/opus'] }] }
  }
  const asked: string[] = []
  vi.stubGlobal('confirm', (q: string) => (asked.push(q), true))
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-delete-profile]').trigger('click')
  await flushPromises()
  expect(asked[1]).toContain('claude is used by the roles CEO, PM, developer and reviewer. Delete it and take its models out of these roles?')
  expect(s.sent.map((x) => [x.method, x.path, x.ifMatch])).toEqual([
    ['DELETE', '/api/profiles/claude', '0123456789abcdef'],
    ['DELETE', '/api/profiles/claude?detach=true', '0123456789abcdef'],
  ])
  expect(w.findComponent(ProfileEditor).emitted('deleted')).toEqual([
    ['Profile claude deleted. Its models were taken out of the roles CEO (claude/sonnet) and developer (claude/sonnet, claude/opus).'],
  ])
})

test('editor: keeping a profile that roles use sends nothing more', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = () => {
    throw new ApiError(409, 'in_use', 'profile claude is used by the roles developer', { roles: ['developer'] })
  }
  let n = 0
  vi.stubGlobal('confirm', () => n++ === 0)
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-delete-profile]').trigger('click')
  await flushPromises()
  expect(s.sent).toHaveLength(1)
  expect(w.find('[data-delete-error]').exists()).toBe(false)
  expect(w.findComponent(ProfileEditor).emitted()).not.toHaveProperty('deleted')
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

test('editor: a refusal of the environment as a whole shows under its table', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.state.write = () => {
    throw new ApiError(422, 'validation', 'env holds too many variables', { field: 'env' })
  }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  await w.get('[data-command]').setValue('claude2')
  await saveButton(w).trigger('click')
  await flushPromises()
  expect(w.find('[data-form-errors]').exists()).toBe(false)
  expect(w.findAll('[data-env-table]')[0]!.text()).toContain('env holds too many variables')
})

test('editor: the usage scripts are read again after a snapshot, and an older answer never replaces a newer one', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  const answers: ((names: string[]) => void)[] = []
  const client = s.session.client
  const base = client.get.bind(client)
  client.get = ((path: string, q?: never): Promise<unknown> => {
    if (path === '/api/usage/scripts') return new Promise((res) => answers.push((names) => res({ data: names.map((name) => ({ name })), eventId: null })))
    if (path === '/api/settings') return Promise.resolve({ data: { settings: { usage: { refresh_interval: '5m' } } }, eventId: null })
    return base(path, q)
  }) as unknown as typeof client.get
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  const offered = () => [...w.get<HTMLSelectElement>('[data-usage-script]').element.options].map((o) => o.value)
  expect(answers).toHaveLength(1)
  // A snapshot reads again; that newer answer comes first, the older one after it.
  s.omo.stream!.onResync(s.omo.eventId)
  await flushPromises()
  expect(answers.length).toBeGreaterThanOrEqual(2)
  answers.at(-1)!(['claude-usage', 'new-one'])
  await flushPromises()
  answers[0]!(['claude-usage'])
  await flushPromises()
  expect(offered()).toEqual(['', 'claude-usage', 'new-one'])
})

test("editor: a model's usage note shows beside its windows, and an empty list says extra_usage does not count", async () => {
  const s = await setup()
  s.state.profiles = { profiles: [claude()], errors: [] }
  s.omo.usage = {
    readings: [],
    models: [{ model: 'claude/sonnet', state: 'unknown', window: '', used_percent: null, resets_at: null, note: 'claude/sonnet counts the window 5h; the script did not report it, so its usage is unknown' }],
  }
  const w = await mountIn(s, ProfileEditor, { name: 'claude' })
  expect(w.get('[data-model-editor="sonnet"] [data-model-usage-note]').text()).toBe('claude/sonnet counts the window 5h; the script did not report it, so its usage is unknown.')
  expect(w.get('[data-model-editor="sonnet"] [data-usage-windows]').text()).toContain('A window the account does not have is ignored')
  await w.get('[data-model-editor="sonnet"] [data-usage-windows]').get('[data-part="remove"]').trigger('click')
  expect(w.get('[data-model-editor="sonnet"] [data-usage-windows]').text()).toContain('every window of the script counts except extra_usage')
})

const codex = (): Profile => ({
  name: 'codex',
  cli: 'codex',
  command: 'codex',
  args: [],
  env: {},
  account_dir: '',
  usage_script: '',
  prompt_delivery: {},
  models: { sol: { args: ['--model', 'gpt-6.1-sol'], env: {}, usage_windows: [], description: 'Mine.' } },
  revision: '1111111111111111',
})

test('editor: "Add missing models" adds the suggested models of a known program, says which and shows them', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [codex()], errors: [] }
  const after: Profile = {
    ...codex(),
    models: { ...codex().models, luna: { args: ['--model', 'gpt-5.6-luna'], env: {}, usage_windows: [], description: 'Fast.' } },
    revision: '2222222222222222',
  }
  s.state.write = (sent) => {
    s.state.profiles = { profiles: [after], errors: [] }
    return sent.path.endsWith('/catalog-models') ? { added: ['luna'], profile: after } : null
  }
  const w = await mountIn(s, ProfileEditor, { name: 'codex' })
  expect(w.get('[data-catalog-models-hint]').text()).toBe("Adds omo's suggested models for Codex that this profile lacks; models it has stay as they are.")
  await w.get('[data-add-catalog-models]').trigger('click')
  await flushPromises()
  expect(s.sent).toEqual([{ method: 'POST', path: '/api/profiles/codex/catalog-models', body: {}, ifMatch: '1111111111111111' }])
  expect(w.get('[data-catalog-models-result]').text()).toBe('Added the model luna.')
  expect(w.find('[data-model-editor="luna"]').exists()).toBe(true)
  expect(saveButton(w).attributes('disabled')).toBeDefined()

  // Nothing missing any more.
  s.state.write = () => ({ added: [], profile: after })
  await w.get('[data-add-catalog-models]').trigger('click')
  await flushPromises()
  expect(w.get('[data-catalog-models-result]').text()).toBe("No model was missing: the profile has every model omo suggests for Codex.")
})

test('editor: "Add missing models" waits for unsaved changes, and is not offered for a custom program', async () => {
  const s = await setup()
  s.state.profiles = { profiles: [codex(), { ...claude(), name: 'mine', cli: 'custom' }], errors: [] }
  const w = await mountIn(s, ProfileEditor, { name: 'codex' })
  await w.get('[data-command]').setValue('codex2')
  const button = w.get('[data-add-catalog-models]')
  expect(button.attributes('disabled')).toBeDefined()
  const reason = w.get('[data-catalog-models-blocked]')
  expect(reason.text()).toBe('Save or discard your changes first.')
  expect(button.attributes('aria-describedby')).toContain(reason.attributes('id'))

  const custom = await mountIn(s, ProfileEditor, { name: 'mine' })
  expect(custom.find('[data-add-catalog-models]').exists()).toBe(false)
})
