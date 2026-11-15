import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { ApiResult } from '../../../api/client'
import type { PluginInfo } from '../../../api/types'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import InstallDialog from './InstallDialog.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const info = (o: Partial<PluginInfo> = {}): PluginInfo => ({ name: 'log', version: '1.0.0', description: '', state: 'ready', message: '', commit: 'abc', last_event: '', last_run_at: null, hooks: [], ...o })

async function open(props: Record<string, unknown> = {}): Promise<{ w: VueWrapper; h: Harness }> {
  const h = await harness()
  const w = await mountWith(h, InstallDialog, { open: true, ...props })
  mounted = w
  await flushPromises()
  return { w, h }
}

const submit = async (w: VueWrapper) => {
  await w.get('form').trigger('submit')
  await flushPromises()
}

test('says that a plugin runs as the user and shows the address as typed, as text', async () => {
  const { w } = await open()
  expect(w.get('[data-install-warning]').text()).toContain('runs on this computer as you')
  expect(w.find('[data-install-address]').exists()).toBe(false)
  await w.get('[data-install-source]').setValue('/home/me/<b>log</b>‮.git')
  expect(w.get('[data-install-address]').text()).toBe('/home/me/<b>log</b>\\u{202E}.git')
  expect(w.find('[data-install-address] b').exists()).toBe(false)
})

test('the name follows the source until the user types one', async () => {
  const { w } = await open()
  await w.get('[data-install-source]').setValue('https://example.invalid/me/omo-ticket-log.git')
  expect((w.get('[data-install-name]').element as HTMLInputElement).value).toBe('omo-ticket-log')
  await w.get('[data-install-name]').setValue('mine')
  await w.get('[data-install-source]').setValue('https://example.invalid/me/other.git')
  expect((w.get('[data-install-name]').element as HTMLInputElement).value).toBe('mine')
})

test('installing sends name, source, subpath and ref once, says it is busy and reports the plugin', async () => {
  const installed: PluginInfo[] = []
  const { w, h } = await open({ onInstalled: (i: PluginInfo) => installed.push(i) })
  let release!: () => void
  h.respond.write = () => new Promise<ApiResult<unknown>>((res) => (release = () => res(ok(info()))))
  await w.get('[data-install-source]').setValue(' /home/me/log ')
  await w.get('form').trigger('submit')
  await w.get('form').trigger('submit')
  await flushPromises()
  const button = w.get('[data-install-submit]')
  expect(button.text()).toBe('Installing…')
  expect(button.attributes('disabled')).toBeDefined()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/plugins', body: { name: 'log', source: '/home/me/log', subpath: '', ref: '', enabled: true, config: {} } }])
  release()
  await flushPromises()
  expect(installed).toEqual([info()])
})

test('a refused field is shown under it; another refusal and "another operation is running" above the fields', async () => {
  const { w, h } = await open()
  await w.get('[data-install-source]').setValue('ext::sh')
  h.respond.write = () => refuse('validation', 'source must be builtin:<name>, an https:// or ssh:// address', { field: 'source' })
  await submit(w)
  const field = w.get('[data-install-source]').element.closest('.omo-field')!
  expect(field.textContent).toContain('source must be builtin:<name>, an https:// or ssh:// address')
  expect(w.get('[data-install-source]').attributes('aria-invalid')).toBe('true')
  expect(w.find('[data-install-error]').exists()).toBe(false)

  h.respond.write = () => refuse('conflict', 'plugin log is being installed, updated, changed or removed; try again when that is done', undefined, 409)
  await submit(w)
  expect(w.get('[data-install-error]').text()).toContain('plugin log is being installed, updated, changed or removed; try again when that is done')
  expect(w.get('[data-install-source]').attributes('aria-invalid')).toBeUndefined()
  expect(w.get('[data-install-submit]').attributes('disabled')).toBeUndefined()

  h.respond.write = () => refuse('validation', 'the plugin could not be installed: hook 1: event "prompt_render" is no longer available')
  await submit(w)
  expect(w.get('[data-install-error]').text()).toContain('event "prompt_render" is no longer available')
})

test('while installing the dialog does not close: Cancel is disabled and saying close shows why; the refusal is then seen in place', async () => {
  let closes = 0
  const { w, h } = await open({ onClose: () => closes++ })
  let release!: () => void
  h.respond.write = () =>
    new Promise<ApiResult<unknown>>((_res, fail) => {
      release = () => {
        try {
          refuse('validation', 'the plugin could not be installed: no manifest')
        } catch (e) {
          fail(e)
        }
      }
    })
  await w.get('[data-install-source]').setValue('/home/me/log')
  await submit(w)
  const cancel = w.get('[data-install-cancel]')
  expect(cancel.attributes('disabled')).toBeDefined()
  // Escape, or the dialog's own Close button.
  await w.get('dialog').trigger('keydown', { key: 'Escape' })
  await w.get('dialog button[aria-label="Close"]').trigger('click')
  await flushPromises()
  expect(closes).toBe(0)
  expect(w.get('[data-install-busy]').text()).toContain('omo is still installing the plugin; the dialog closes when omo has answered.')
  release()
  await flushPromises()
  expect(w.find('[data-install-busy]').exists()).toBe(false)
  expect(w.get('[data-install-error]').text()).toContain('no manifest')
  expect(cancel.attributes('disabled')).toBeUndefined()
  await cancel.trigger('click')
  expect(closes).toBe(1)
})
