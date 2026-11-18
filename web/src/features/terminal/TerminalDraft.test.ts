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
  const send = vi.fn(async (_paste: string) => {})
  const w = mount(TerminalDraft, {
    props: { draftId: 't1', label: 'Message to the agent', bracketed: () => true, send, ...props },
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
  test('Enter sends the whole draft as one bracketed paste; Shift+Enter makes a line break and sends nothing', async () => {
    const { w, send } = box()
    await field(w).setValue('one')
    await field(w).trigger('keydown', { key: 'Enter', shiftKey: true })
    expect(send).not.toHaveBeenCalled()
    await field(w).setValue('one\ntwo')
    const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    field(w).element.dispatchEvent(enter)
    expect(enter.defaultPrevented).toBe(true)
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith('\x1b[200~one\rtwo\x1b[201~'))
    await vi.waitFor(() => expect((field(w).element as HTMLTextAreaElement).value).toBe(''))
  })

  test('Enter while an input method composes a character sends nothing', async () => {
    const { w, send } = box()
    await field(w).setValue('ni')
    await field(w).trigger('keydown', { key: 'Enter', isComposing: true })
    expect(send).not.toHaveBeenCalled()
  })

  test('without bracketed paste the line breaks are plain Enter presses', async () => {
    const { w, send } = box({ bracketed: () => false })
    await field(w).setValue('a\nb')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith('a\rb'))
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
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(w.find('[data-draft-error]').text()).toContain('did not confirm'))
    expect((field(w).element as HTMLTextAreaElement).value).toBe('keep')
    expect(w.findAll('[data-draft-attachment]')).toHaveLength(1)
  })

  test('a pasted image is stored by omo, shown in the draft, can be removed, and its path goes with the message', async () => {
    const session = sessionWith()
    const upload = stubUpload(session)
    const { w, send } = box({}, session)
    const ev = paste(field(w).element, [png()])
    expect(ev.defaultPrevented).toBe(true)
    await vi.waitFor(() => expect(w.findAll('[data-draft-attachment]')).toHaveLength(1))
    expect(upload).toHaveBeenCalledWith('/api/terminal-images', expect.any(File), { 'Omo-Image-Name': 'shot%20one.png' })
    expect(w.find('[data-draft-attachment]').text()).toContain('shot one.png')
    await field(w).setValue('see this')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith("\x1b[200~see this\r'/home/me/omo/images/terminal/a b.png'\x1b[201~"))
    expect(w.findAll('[data-draft-attachment]')).toHaveLength(0)
  })

  test('a removed image is not sent; dropped images work like pasted ones', async () => {
    const session = sessionWith()
    stubUpload(session)
    const { w, send } = box({}, session)
    drop(w.find('[data-terminal-draft]').element, [png()])
    await vi.waitFor(() => expect(w.findAll('[data-draft-attachment]')).toHaveLength(1))
    await w.find('[data-draft-remove]').trigger('click')
    expect(w.findAll('[data-draft-attachment]')).toHaveLength(0)
    await field(w).setValue('text only')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(send).toHaveBeenCalledWith('\x1b[200~text only\x1b[201~'))
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
    first.w.unmount()
    expect(loadDraft('t1').text).toBe('unfinished')
    const again = box({}, session)
    expect((field(again.w).element as HTMLTextAreaElement).value).toBe('unfinished')
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
    await w.setProps({ label: 'Message to the agent (renamed)', bracketed: () => false })
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
})
