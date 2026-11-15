import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { expect, test, vi } from 'vitest'
import { ApiError } from '../../../api/client'
import type { UsageScript } from '../../../api/types'
import ScriptEditor from './ScriptEditor.vue'
import { NEW_SCRIPT } from './usageForms'
import { mountIn, setup, type Sent } from './usageTesting'

const claude = (over: Partial<UsageScript> = {}): UsageScript => ({
  name: 'claude',
  text: 'function usage(account)\n  return {}\nend\n',
  default: 'function usage(account)\n  -- omo\'s own\nend\n',
  has_default: true,
  customized: true,
  revision: '0123456789abcdef',
  ...over,
})

function button(w: VueWrapper, text: string) {
  const b = w.findAll('button').find((x) => x.text() === text)
  if (!b) throw new Error(`no button ${text}`)
  return b
}

test('editor: a new script starts from the template and saves with If-Match 0', async () => {
  const s = await setup()
  s.state.write = (sent: Sent) => claude({ name: 'mine', text: (sent.body as { text: string }).text, has_default: false, revision: 'aaaaaaaaaaaaaaaa' })
  const w = await mountIn(s, ScriptEditor, { name: 'mine', isNew: true })
  expect(w.text()).toContain('Script mine (not saved yet)')
  const area = w.get<HTMLTextAreaElement>('[data-script-text]')
  expect(area.element.value).toBe(NEW_SCRIPT)
  expect(area.attributes('wrap')).toBe('off')
  await area.setValue('function usage(account)\n  return { { name = "a", used = 1 } }\nend\n')
  await button(w, 'Save').trigger('click')
  await flushPromises()

  expect(s.sent).toEqual([
    { method: 'PUT', path: '/api/usage/scripts/mine', body: { text: 'function usage(account)\n  return { { name = "a", used = 1 } }\nend\n' }, ifMatch: '0' },
  ])
  expect(w.text()).not.toContain('(not saved yet)')
  // A script without a default can be deleted, not reset.
  expect(w.find('[data-script-reset]').text()).toBe('Delete mine…')
  expect(w.find('[data-script-default-toggle]').exists()).toBe(false)
})

test('editor: a refused script shows the message under the editor', async () => {
  const s = await setup()
  s.state.get['/api/usage/scripts/claude'] = () => claude()
  s.state.write = () => {
    throw new ApiError(422, 'validation', 'line 2: unexpected symbol near <eof>', { field: 'text' })
  }
  const w = await mountIn(s, ScriptEditor, { name: 'claude' })
  await w.get('[data-script-text]').setValue('function usage(\n')
  await button(w, 'Save').trigger('click')
  await flushPromises()

  expect(s.sent[0]!.ifMatch).toBe('0123456789abcdef')
  const field = w.get('[data-script-text]').element.closest('.omo-field')!
  expect(field.querySelector('.omo-field__error')!.textContent).toBe('line 2: unexpected symbol near <eof>')
  expect(w.get('[data-script-text]').attributes('aria-invalid')).toBe('true')
  expect(w.find('[data-form-errors]').exists()).toBe(false)
})

test("editor: reset asks first, and only a yes resets to omo's default", async () => {
  const s = await setup()
  let stored = claude()
  s.state.get['/api/usage/scripts/claude'] = () => stored
  s.state.write = () => {
    stored = claude({ text: stored.default, customized: false, revision: 'bbbbbbbbbbbbbbbb' })
    return null
  }
  const confirm = vi.fn(() => false)
  vi.stubGlobal('confirm', confirm)
  const w = await mountIn(s, ScriptEditor, { name: 'claude' })

  await w.get('[data-script-reset]').trigger('click')
  await flushPromises()
  expect(confirm).toHaveBeenCalledTimes(1)
  expect((confirm.mock.calls[0] as unknown as [string])[0]).toContain("Reset to omo's default?")
  expect(s.sent).toEqual([])

  confirm.mockReturnValue(true)
  await w.get('[data-script-reset]').trigger('click')
  await flushPromises()
  expect(s.sent).toEqual([{ method: 'DELETE', path: '/api/usage/scripts/claude', body: null, ifMatch: '0123456789abcdef' }])
  expect(w.get<HTMLTextAreaElement>('[data-script-text]').element.value).toBe(stored.default)
  expect(w.find('[data-script-reset]').exists()).toBe(false)
  vi.unstubAllGlobals()
})

test('editor: deleting a script a profile uses shows the server message in place', async () => {
  const s = await setup()
  s.state.get['/api/usage/scripts/proxy'] = () => claude({ name: 'proxy', has_default: false, default: '' })
  s.state.write = () => {
    throw new ApiError(409, 'in_use', 'the usage script proxy is used by the profiles work, home')
  }
  vi.stubGlobal('confirm', () => true)
  const w = await mountIn(s, ScriptEditor, { name: 'proxy' })
  await w.get('[data-script-reset]').trigger('click')
  await flushPromises()
  expect(w.get('[data-reset-error]').text()).toContain('the usage script proxy is used by the profiles work, home')
  vi.unstubAllGlobals()
})

test("editor: Show default shows omo's text as text", async () => {
  const s = await setup()
  s.state.get['/api/usage/scripts/claude'] = () => claude({ default: 'x <b>bold</b>‮' })
  const w = await mountIn(s, ScriptEditor, { name: 'claude' })
  await w.get('[data-script-default-toggle]').trigger('click')
  const pre = w.get('[data-script-default] pre')
  expect(pre.find('b').exists()).toBe(false)
  expect(pre.text()).toContain('x <b>bold</b>')
  expect(pre.text()).not.toContain('‮')
})

test('editor: says it is busy while its test runs, so the page keeps it open', async () => {
  const s = await setup()
  s.state.get['/api/usage/scripts/claude'] = () => claude()
  s.state.get['/api/profiles'] = () => ({ profiles: [], errors: [] })
  let release!: () => void
  s.state.write = () => new Promise((res) => (release = () => res({ windows: [], error: '', log: [], millis: 1 })))
  const busy: boolean[] = []
  const w = await mountIn(s, ScriptEditor, { name: 'claude', onBusy: (b: boolean) => busy.push(b) })
  await w.get('[data-test-run]').trigger('click')
  await flushPromises()
  expect(busy).toEqual([true])
  release()
  await flushPromises()
  expect(busy).toEqual([true, false])
})
