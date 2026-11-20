import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createTokenStore } from '../../auth/token'
import { createSession, SESSION, type Session } from '../../session'
import { FakeOmo } from '../../testing/fakeOmo'
import TerminalDraft from './TerminalDraft.vue'
import { loadDraft } from './draftStore'

const live: Array<{ unmount(): void }> = []
function sessionWith(): Session {
  const omo = new FakeOmo()
  return createSession({ tokens: createTokenStore(() => window.localStorage), makeClient: omo.makeClient, connect: omo.connect })
}
function box(props: Record<string, unknown> = {}, session: Session = sessionWith()) {
  const send = vi.fn(async (_message: string[]) => {})
  const w = mount(TerminalDraft, {
    props: { draftId: 't1', label: 'Message to the agent', send, ...props },
    attachTo: document.body,
    global: { provide: { [SESSION as symbol]: session } },
  })
  live.push(w)
  return { w, send, session }
}
const field = (w: ReturnType<typeof box>['w']) => w.find('[data-draft-field]')
const png = () => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], 'shot one.png', { type: 'image/png' })
function paste(target: Element, files: File[], text = ''): Event {
  const ev = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(ev, 'clipboardData', { value: { files, types: files.length ? ['Files'] : ['text/plain'], getData: () => text } })
  target.dispatchEvent(ev)
  return ev
}
function drop(target: Element, files: File[]): void {
  const ev = new Event('drop', { bubbles: true, cancelable: true })
  Object.defineProperty(ev, 'dataTransfer', { value: { files, types: ['Files'], getData: () => '' } })
  target.dispatchEvent(ev)
}
function stubUpload(session: Session) {
  return vi.spyOn(session.client, 'upload').mockResolvedValue({
    data: { path: '/home/me/omo/images/terminal/a b.png', name: 'shot one.png', content_type: 'image/png', size: 4 },
  } as never)
}

beforeEach(() => window.localStorage.clear())
afterEach(() => {
  live.splice(0).forEach((w) => w.unmount())
  vi.restoreAllMocks()
})

describe('TerminalDraft', () => {
  test('Enter sends the whole draft as one message; Shift+Enter makes a line break and sends nothing', async () => {
    const { w, send } = box()
    await field(w).setValue('one')
    await field(w).trigger('keydown', { key: 'Enter', shiftKey: true })
    expect(send).not.toHaveBeenCalled()
    await field(w).setValue('one\ntwo')
    const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    field(w).element.dispatchEvent(enter)
    expect(enter.defaultPrevented).toBe(true)
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith(['one\ntwo']))
    await vi.waitFor(() => expect((field(w).element as HTMLTextAreaElement).value).toBe(''))
  })

  test('Enter while an input method composes a character sends nothing', async () => {
    const { w, send } = box()
    await field(w).setValue('ni')
    await field(w).trigger('keydown', { key: 'Enter', isComposing: true })
    expect(send).not.toHaveBeenCalled()
  })

  test('an empty draft sends nothing', async () => {
    const { w, send } = box()
    await field(w).setValue('  \n ')
    await w.find('[data-draft-send]').trigger('click')
    expect(send).not.toHaveBeenCalled()
  })

  test('a failed send keeps the draft and its images, and shows the error', async () => {
    const session = sessionWith()
    stubUpload(session)
    const send = vi.fn().mockRejectedValue(new Error('The terminal did not confirm the message.'))
    const { w } = box({ send }, session)
    await field(w).setValue('keep')
    paste(field(w).element, [png()])
    await vi.waitFor(() => expect(w.findAll('[data-draft-attachment]')).toHaveLength(1))
    await vi.waitFor(() => expect(w.find('[data-draft-send]').attributes('disabled')).toBeUndefined())
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(w.find('[data-draft-error]').text()).toContain('did not confirm'))
    expect((field(w).element as HTMLTextAreaElement).value).toBe('keep[image 1]')
    expect(w.findAll('[data-draft-attachment]')).toHaveLength(1)
  })

  test('a pasted image is stored by omo, shown in the draft, can be removed, and its path goes with the message', async () => {
    const session = sessionWith()
    const upload = stubUpload(session)
    const { w, send } = box({}, session)
    const ev = paste(field(w).element, [png()])
    expect(ev.defaultPrevented).toBe(true)
    await vi.waitFor(() => expect(w.findAll('[data-draft-attachment]')).toHaveLength(1))
    await vi.waitFor(() => expect(w.find('[data-draft-send]').attributes('disabled')).toBeUndefined())
    expect(upload).toHaveBeenCalledWith('/api/terminal-images', expect.any(File), { 'Omo-Image-Name': 'shot%20one.png' })
    expect(w.find('[data-draft-attachment]').text()).toContain('shot one.png')
    const el = field(w).element as HTMLTextAreaElement
    el.selectionStart = el.selectionEnd = 0
    paste(el, [], 'see this ')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith(["see this ", "'/home/me/omo/images/terminal/a b.png'"]))
    expect(w.findAll('[data-draft-attachment]')).toHaveLength(0)
  })

  test('a removed image is not sent; dropped images work like pasted ones', async () => {
    const session = sessionWith()
    stubUpload(session)
    const { w, send } = box({}, session)
    drop(w.find('[data-terminal-draft]').element, [png()])
    await vi.waitFor(() => expect(w.findAll('[data-draft-attachment]')).toHaveLength(1))
    await vi.waitFor(() => expect(w.find('[data-draft-send]').attributes('disabled')).toBeUndefined())
    await w.find('[data-draft-remove]').trigger('click')
    expect(w.findAll('[data-draft-attachment]')).toHaveLength(0)
    await field(w).setValue('text only')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith(['text only']))
  })

  test('files that are not images are refused with a word and not uploaded', async () => {
    const session = sessionWith()
    const upload = vi.spyOn(session.client, 'upload')
    const { w } = box({}, session)
    drop(w.find('[data-terminal-draft]').element, [new File(['x'], 'notes.txt', { type: 'text/plain' })])
    await vi.waitFor(() => expect(w.find('[data-draft-error]').text()).toBe('Only images can be dropped here.'))
    expect(upload).not.toHaveBeenCalled()
  })

  test('pasted text is cleaned of control characters and lands at the cursor', async () => {
    const { w } = box()
    const el = field(w).element as HTMLTextAreaElement
    await field(w).setValue('ab')
    el.selectionStart = el.selectionEnd = 1
    const ev = paste(el, [], 'X\x1b[201~Y')
    expect(ev.defaultPrevented).toBe(true)
    await nextTick()
    expect(el.value).toBe('aX[201~Yb')
  })

  test('the draft is kept per terminal, survives the box going away, and is restored', async () => {
    const session = sessionWith()
    stubUpload(session)
    const first = box({}, session)
    await field(first.w).setValue('unfinished')
    paste(field(first.w).element, [png()])
    await vi.waitFor(() => expect(first.w.findAll('[data-draft-attachment]')).toHaveLength(1))
    await vi.waitFor(() => expect(first.w.find('[data-draft-send]').attributes('disabled')).toBeUndefined())
    first.w.unmount()
    expect(loadDraft('t1').text).toBe('unfinished[image 1]')
    const again = box({}, session)
    expect((field(again.w).element as HTMLTextAreaElement).value).toBe('unfinished[image 1]')
    expect(again.w.findAll('[data-draft-attachment]')).toHaveLength(1)
    const other = box({ draftId: 't2' }, session)
    expect((field(other.w).element as HTMLTextAreaElement).value).toBe('')
  })

  test('a sent draft is gone from storage too', async () => {
    const { w, send } = box()
    await field(w).setValue('bye')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalled())
    await vi.waitFor(() => expect(loadDraft('t1').text).toBe(''))
  })

  test('re-rendering from outside never changes the draft or takes the focus', async () => {
    const { w } = box()
    const el = field(w).element as HTMLTextAreaElement
    el.focus()
    await field(w).setValue('typing')
    await w.setProps({ label: 'Message to the agent (renamed)' })
    expect(el.value).toBe('typing')
    expect(document.activeElement).toBe(el)
  })

  test('a draft over the limit is refused before anything is sent', async () => {
    const { w, send } = box()
    await field(w).setValue('x'.repeat(13_000))
    await w.find('[data-draft-send]').trigger('click')
    expect(send).not.toHaveBeenCalled()
    expect(w.find('[data-draft-error]').text()).toContain('at most')
  })

  test('the focus returns to the box after sending', async () => {
    const { w, send } = box()
    await field(w).setValue('hi')
    ;(w.find('[data-draft-send]').element as HTMLButtonElement).focus()
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalled())
    await vi.waitFor(() => expect(document.activeElement).toBe(field(w).element))
  })

  test('a paste, a dropped image or a change while a message is on its way is not lost with it: only what was sent is cleared', async () => {
    const session = sessionWith()
    const upload = stubUpload(session)
    let release!: () => void
    const send = vi.fn(() => new Promise<void>((resolve) => (release = resolve)))
    const { w } = box({ send }, session)
    await field(w).setValue('the message')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalled())
    // In flight: the box takes no paste and no drop, so nothing is added that the message lacks.
    paste(field(w).element, [], 'late text')
    drop(w.find('[data-terminal-draft]').element, [png()])
    await nextTick()
    expect(upload).not.toHaveBeenCalled()
    expect((field(w).element as HTMLTextAreaElement).value).toBe('the message')
    release()
    await vi.waitFor(() => expect((field(w).element as HTMLTextAreaElement).value).toBe(''))
  })

  test('if the text was changed by other means while sending (another view), it is kept', async () => {
    let release!: () => void
    const send = vi.fn(() => new Promise<void>((resolve) => (release = resolve)))
    const { w } = box({ send })
    await field(w).setValue('sent text')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalled())
    ;(field(w).element as HTMLTextAreaElement).value = 'sent text and more'
    await field(w).trigger('input')
    release()
    await vi.waitFor(() => expect(w.find('[data-draft-send]').text()).toBe('Send'))
    expect((field(w).element as HTMLTextAreaElement).value).toBe('sent text and more')
  })
})

test('image paste reserves its cursor position while uploading and Backspace removes the whole marker', async () => {
  const session = sessionWith()
  let finish!: (value: unknown) => void
  vi.spyOn(session.client, 'upload').mockImplementation(() => new Promise((resolve) => { finish = resolve as typeof finish }) as never)
  const { w, send } = box({}, session)
  await field(w).setValue('before after')
  const el = field(w).element as HTMLTextAreaElement
  el.setSelectionRange(7, 7)
  paste(el, [png()])
  await nextTick()
  expect(el.value).toBe('before [image 1]after')
  el.setSelectionRange(el.value.length, el.value.length)
  paste(el, [], ' tail')
  finish({ data: { path: '/image.png', name: 'shot one.png' } })
  await vi.waitFor(() => expect(w.find('[data-draft-send]').attributes('disabled')).toBeUndefined())
  expect(el.value).toBe('before [image 1]after tail')
  el.setSelectionRange(16, 16)
  await field(w).trigger('keydown', { key: 'Backspace' })
  expect(el.value).toBe('before after tail')
  expect(w.findAll('[data-draft-attachment]')).toHaveLength(0)
  await w.find('[data-draft-send]').trigger('click')
  await vi.waitFor(() => expect(send).toHaveBeenCalledWith(['before after tail']))
})

test('deleting an uploading marker does not restore its image when the upload finishes', async () => {
  const session = sessionWith()
  let finish!: (value: unknown) => void
  vi.spyOn(session.client, 'upload').mockImplementation(() => new Promise((resolve) => { finish = resolve as typeof finish }) as never)
  const { w } = box({}, session)
  paste(field(w).element, [png()])
  await nextTick()
  const el = field(w).element as HTMLTextAreaElement
  el.setSelectionRange(el.value.length, el.value.length)
  await field(w).trigger('keydown', { key: 'Backspace' })
  expect(el.value).toBe('')
  finish({ data: { path: '/image.png', name: 'shot one.png' } })
  await vi.waitFor(() => expect(w.find('[data-draft-send]').attributes('disabled')).toBeUndefined())
  expect(el.value).toBe('')
  expect(w.findAll('[data-draft-attachment]')).toHaveLength(0)
})

test('native typing next to a marker uses the actual selection even when the text repeats its label', async () => {
  const session = sessionWith()
  stubUpload(session)
  const { w, send } = box({}, session)
  paste(field(w).element, [png()])
  await vi.waitFor(() => expect(w.find('[data-draft-send]').attributes('disabled')).toBeUndefined())
  const el = field(w).element as HTMLTextAreaElement
  el.setSelectionRange(0, 0)
  el.dispatchEvent(new InputEvent('beforeinput', { inputType: 'insertText', data: '[', bubbles: true, cancelable: true }))
  el.value = '[[image 1]'
  el.setSelectionRange(1, 1)
  el.dispatchEvent(new InputEvent('input', { inputType: 'insertText', data: '[', bubbles: true }))
  await nextTick()
  expect(el.value).toBe('[[image 1]')
  expect(w.findAll('[data-draft-attachment]')).toHaveLength(1)
  await w.find('[data-draft-send]').trigger('click')
  await vi.waitFor(() => expect(send).toHaveBeenCalledWith(['[', "'/home/me/omo/images/terminal/a b.png'"]))
})
