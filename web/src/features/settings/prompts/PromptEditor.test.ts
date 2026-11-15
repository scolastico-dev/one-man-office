import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { ApiError, type ApiResult, type SendOptions } from '../../../api/client'
import type { Prompt, PromptPreview } from '../../../api/types'
import { harness, mountWith, type Harness } from '../../create/dialogTesting'
import PromptEditor from './PromptEditor.vue'
import PromptsPage from './PromptsPage.vue'

let w: VueWrapper | null = null
afterEach(() => {
  w?.unmount()
  w = null
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

interface Call {
  method: string
  path: string
  body: unknown
  ifMatch?: string
}

interface World {
  h: Harness
  calls: Call[]
  prompts: Map<string, Prompt>
  previews: (name: string, text: string) => PromptPreview | Promise<PromptPreview>
  refuse?: (c: Call) => void
}

/** The browser's own question (no question host is mounted here); answers `answer`. */
function stubConfirm(answer: boolean) {
  const fn = vi.fn().mockReturnValue(answer)
  vi.stubGlobal('confirm', fn)
  return fn
}

const SAVE = '[aria-label="Save changes"] .omo-btn--primary'

const ok = <T,>(data: T): ApiResult<T> => ({ data, eventId: 5 })

function prompt(name: string, o: Partial<Prompt> = {}): Prompt {
  return { name, text: 'omo text', customized: false, kind: 'message', default: 'omo text', revision: 'aa11', ...o }
}

async function world(prompts: Prompt[]): Promise<World> {
  const h = await harness()
  const wd: World = { h, calls: [], prompts: new Map(prompts.map((p) => [p.name, p])), previews: () => ({ text: '', error: '' }) }
  const client = h.session.client as unknown as Record<string, unknown>
  const baseGet = h.session.client.get.bind(h.session.client)
  client.get = async (path: string, query?: never) => {
    if (path === '/api/prompts') {
      return ok([...wd.prompts.values()].map((p) => ({ name: p.name, customized: p.customized, kind: p.kind, revision: p.revision })))
    }
    const m = /^\/api\/prompts\/([^/]+)$/.exec(path)
    if (m) {
      const p = wd.prompts.get(m[1]!)
      if (!p) throw new ApiError(404, 'not_found', 'no prompt')
      return ok({ ...p })
    }
    return baseGet(path, query)
  }
  client.send = async (method: string, path: string, body?: unknown, opts?: SendOptions) => {
    const c: Call = { method, path, body, ifMatch: opts?.ifMatch }
    wd.calls.push(c)
    const pm = /^\/api\/prompts\/([^/]+)\/preview$/.exec(path)
    if (pm) return ok(await wd.previews(pm[1]!, (body as { text: string }).text))
    wd.refuse?.(c)
    const m = /^\/api\/prompts\/([^/]+)$/.exec(path)!
    const p = wd.prompts.get(m[1]!)!
    if (method === 'PUT') {
      const text = (body as { text: string }).text
      const next = { ...p, text, customized: text !== p.default && !(p.kind === 'instructions' && text === ''), revision: 'bb22' }
      wd.prompts.set(p.name, next)
      return ok(next)
    }
    wd.prompts.set(p.name, { ...p, text: p.default, customized: false, revision: 'cc33' })
    return ok(undefined)
  }
  return wd
}

async function mountEditor(wd: World, name: string) {
  w = await mountWith(wd.h, PromptEditor, { name, title: name })
  await flushPromises()
  return w
}

const area = () => w!.get('textarea')

test('editor: Save sends If-Match with the revision it was built on', async () => {
  const wd = await world([prompt('stalled', { text: 'old', default: 'old' })])
  await mountEditor(wd, 'stalled')
  expect(area().element.value).toBe('old')
  await area().setValue('new text {{.Silent}}')
  await w!.get(SAVE).trigger('click')
  await flushPromises()
  const put = wd.calls.find((c) => c.method === 'PUT')!
  expect(put).toMatchObject({ path: '/api/prompts/stalled', body: { text: 'new text {{.Silent}}' }, ifMatch: 'aa11' })
  expect(w!.text()).toContain('Saved')
})

test('editor: a template error shows under the editor in the server\'s words and moves the focus there', async () => {
  const wd = await world([prompt('stalled')])
  wd.refuse = (c) => {
    if (c.method === 'PUT') throw new ApiError(422, 'validation', 'template: stalled:1: function "nme" not defined', { field: 'text' })
  }
  await mountEditor(wd, 'stalled')
  await area().setValue('{{nme}}')
  await w!.get(SAVE).trigger('click')
  await flushPromises()
  expect(area().attributes('aria-invalid')).toBe('true')
  const err = w!.get('.omo-field__error')
  expect(err.text()).toBe('template: stalled:1: function "nme" not defined')
  expect(area().attributes('aria-describedby')).toContain(err.attributes('id'))
  // The text stays as typed.
  expect(area().element.value).toBe('{{nme}}')
})

test('editor: Reset asks first, and only a yes resets', async () => {
  const wd = await world([prompt('stalled', { text: 'mine', customized: true, default: 'omo default' })])
  const confirm = stubConfirm(false)
  await mountEditor(wd, 'stalled')
  const reset = () => w!.get('[data-prompt-reset]')
  await reset().trigger('click')
  await flushPromises()
  expect(confirm).toHaveBeenCalledTimes(1)
  expect(confirm.mock.calls[0]![0]).toContain("Reset to omo's default?")
  expect(wd.calls.filter((c) => c.method === 'DELETE')).toHaveLength(0)

  confirm.mockReturnValue(true)
  await reset().trigger('click')
  await flushPromises()
  expect(wd.calls.find((c) => c.method === 'DELETE')).toMatchObject({ path: '/api/prompts/stalled', ifMatch: 'aa11' })
  expect(area().element.value).toBe('omo default')
  expect(w!.find('[data-prompt-reset]').exists()).toBe(false)
})

test('editor: a failed Reset shows the server\'s message', async () => {
  const wd = await world([prompt('stalled', { text: 'mine', customized: true, default: 'omo default' })])
  stubConfirm(true)
  wd.refuse = (c) => {
    if (c.method === 'DELETE') throw new ApiError(409, 'stale_settings', 'The prompt changed on disk.')
  }
  await mountEditor(wd, 'stalled')
  await w!.get('[data-prompt-reset]').trigger('click')
  await flushPromises()
  expect(w!.get('[data-reset-error]').text()).toContain('The prompt changed on disk.')
})

test('preview shows the server\'s text as text, and a template error in a callout', async () => {
  const wd = await world([prompt('stalled')])
  wd.previews = (_, text) => (text.includes('bad') ? { text: '', error: 'template: stalled:1: function "bad" not defined' } : { text: `<b>bold</b> **x**\n${text}`, error: '' })
  await mountEditor(wd, 'stalled')
  await w!.get('[data-prompt-preview-toggle]').trigger('click')
  await flushPromises()
  const pre = w!.get('[data-preview-text]')
  expect(pre.element.tagName).toBe('PRE')
  expect(pre.text()).toBe('<b>bold</b> **x**\nomo text')
  expect(pre.find('b').exists()).toBe(false)

  vi.useFakeTimers()
  await area().setValue('{{bad}}')
  await vi.advanceTimersByTimeAsync(400)
  await flushPromises()
  expect(w!.get('[data-preview-error]').text()).toContain('template: stalled:1: function "bad" not defined')
  expect(w!.find('[data-preview-text]').exists()).toBe(false)
})

test('an instructions prompt has no template fields and no default; the preview is of the draft', async () => {
  const wd = await world([prompt('developer-instructions', { kind: 'instructions', text: '', default: '' })])
  wd.previews = (name, text) => ({ text: `${name}: ${text}`, error: '' })
  await mountEditor(wd, 'developer-instructions')
  expect(w!.find('[data-template-fields]').exists()).toBe(false)
  expect(w!.find('[data-prompt-default-toggle]').exists()).toBe(false)
  await area().setValue('Run the tests.')
  await w!.get('[data-prompt-preview-toggle]').trigger('click')
  await flushPromises()
  expect(w!.get('[data-preview-text]').text()).toBe('developer-instructions: Run the tests.')
})

test('Show default shows omo\'s text as plain text beside the editor', async () => {
  const wd = await world([prompt('stalled', { text: 'mine', customized: true, default: 'omo <i>default</i>' })])
  await mountEditor(wd, 'stalled')
  await w!.get('[data-prompt-default-toggle]').trigger('click')
  const d = w!.get('[data-prompt-default] pre')
  expect(d.text()).toBe('omo <i>default</i>')
  expect(d.find('i').exists()).toBe(false)
})

test('a hand edit while the editor is open shows the new text; with unsaved edits it keeps them and offers the new value', async () => {
  const wd = await world([prompt('stalled', { text: 'one', default: 'one' })])
  await mountEditor(wd, 'stalled')
  const disk = (text: string, id: number) => {
    wd.prompts.set('stalled', { ...wd.prompts.get('stalled')!, text, customized: true, revision: `d${id}` })
    wd.h.omo.emit('settings.changed', { section: 'prompts', name: 'stalled', source: 'disk' })
  }
  disk('two', 1)
  await flushPromises()
  expect(area().element.value).toBe('two')
  expect(w!.get('[data-changed-on-disk]').text()).toContain('not through omo')

  await area().setValue('mine')
  disk('three', 2)
  await flushPromises()
  expect(area().element.value).toBe('mine')
  expect(w!.find('[data-use-theirs]').exists()).toBe(true)
})

test('page: opening another prompt with unsaved changes asks first', async () => {
  const wd = await world([
    prompt('developer', { kind: 'role', text: 'dev', default: 'dev' }),
    prompt('developer-instructions', { kind: 'instructions', text: '', default: '' }),
    prompt('stalled'),
  ])
  const confirm = stubConfirm(false)
  w = await mountWith(wd.h, PromptsPage, {})
  await flushPromises()
  expect(w.findAll('[data-prompt-row]').map((r) => r.attributes('data-prompt-row'))).toEqual(['developer-instructions', 'developer', 'stalled'])
  expect(w.findAll('[data-prompt-editor]')).toHaveLength(0)

  await w.get('[data-prompt-row="developer-instructions"] [data-prompt-toggle]').trigger('click')
  await flushPromises()
  expect(w.findAll('[data-prompt-editor]')).toHaveLength(1)
  await w.get('textarea').setValue('Run the tests.')

  await w.get('[data-prompt-row="stalled"] [data-prompt-toggle]').trigger('click')
  await flushPromises()
  expect(confirm).toHaveBeenCalledTimes(1)
  expect(w.findAll('[data-prompt-editor]').map((e) => e.attributes('data-prompt-editor'))).toEqual(['developer-instructions'])
  expect(w.get('textarea').element.value).toBe('Run the tests.')

  confirm.mockReturnValue(true)
  await w.get('[data-prompt-row="stalled"] [data-prompt-toggle]').trigger('click')
  await flushPromises()
  expect(w.findAll('[data-prompt-editor]').map((e) => e.attributes('data-prompt-editor'))).toEqual(['stalled'])
  // A clean editor is left without a question.
  await w.get('[data-prompt-row="developer"] [data-prompt-toggle]').trigger('click')
  await flushPromises()
  expect(confirm).toHaveBeenCalledTimes(2)
  expect(w.findAll('[data-prompt-editor]').map((e) => e.attributes('data-prompt-editor'))).toEqual(['developer'])
})
