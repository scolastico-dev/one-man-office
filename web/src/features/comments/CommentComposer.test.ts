import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ApiError } from '../../api/client'
import type { Comment, CommentRequest, Image, Role } from '../../api/types'
import { ticket } from '../board/testing'
import CommentComposer from './CommentComposer.vue'

let w: VueWrapper | null = null
afterEach(() => {
  w?.unmount()
  w = null
})

const story = ticket({ number: 9, type: 'story' })
const agents = [{ session: 's1', role: 'pm' as const, name: 'quiet-heron', state: 'running' as const, purpose: 'work' as const }]

function mountComposer(
  o: {
    send?: (r: CommentRequest) => Promise<Role[]>
    answering?: Comment | null
    t?: typeof story
    ceo?: boolean
    upload?: (file: Blob, name: string) => Promise<Image>
  } = {},
) {
  const sent: CommentRequest[] = []
  w = mount(CommentComposer, {
    attachTo: document.body,
    props: {
      ticket: o.t ?? story,
      context: { ceoEnabled: o.ceo ?? false, agents },
      answering: o.answering ?? null,
      send: o.send ?? (async (r: CommentRequest) => (sent.push(r), [])),
      ...(o.upload ? { upload: o.upload } : {}),
    },
  })
  return { sent }
}
const box = () => w!.get('textarea')
const type = async (s: string) => {
  const el = box().element as HTMLTextAreaElement
  el.value = s
  el.setSelectionRange(s.length, s.length)
  await box().trigger('input')
  await box().trigger('keyup')
}

test('composer: sends only body, question, to, reply_to; keeps the text on a refusal', async () => {
  let fail = true
  const sent: CommentRequest[] = []
  mountComposer({
    send: async (r) => {
      sent.push(r)
      if (fail) throw new ApiError(422, 'invalid_mention', '@ceo cannot be mentioned on story #9: the CEO is off')
      return []
    },
  })
  await type('Hello @ceo')
  await box().trigger('keydown', { key: 'Enter', ctrlKey: true })
  await flushPromises()
  expect(sent).toEqual([{ body: 'Hello @ceo', question: false }])
  expect(Object.keys(sent[0]!)).toEqual(['body', 'question'])
  expect(w!.text()).toContain('@ceo cannot be mentioned on story #9: the CEO is off')
  expect((box().element as HTMLTextAreaElement).value).toBe('Hello @ceo')
  fail = false
  await w!.get('[data-send]').trigger('submit')
  await w!.get('form').trigger('submit')
  await flushPromises()
  expect((box().element as HTMLTextAreaElement).value).toBe('')
})

test('composer: a lost answer says the comment may have been posted and keeps the draft', async () => {
  mountComposer({ send: async () => Promise.reject(new ApiError(0, 'network', 'Cannot reach omo.')) })
  await type('draft')
  await w!.get('form').trigger('submit')
  await flushPromises()
  expect(w!.text()).toContain('may have been posted')
  expect((box().element as HTMLTextAreaElement).value).toBe('draft')
})

test('composer: a question goes to the chosen addressee; an answer carries reply_to', async () => {
  const { sent } = mountComposer({ ceo: true })
  await w!.get('[role="switch"], button[aria-checked]').trigger('click')
  await type('Which one?')
  expect(w!.find('select').exists()).toBe(true)
  expect(w!.findAll('option').map((o) => o.text())).toEqual(['Product manager', 'CEO'])
  await w!.get('select').setValue('ceo')
  await w!.get('form').trigger('submit')
  await flushPromises()
  expect(sent).toEqual([{ body: 'Which one?', question: true, to: 'ceo' }])
  w!.unmount()
  const q = { id: 77 } as Comment
  const again = mountComposer({ answering: q })
  expect(w!.get('[data-answering]').text()).toContain('Answering question #77')
  await type('Use the second.')
  await w!.get('form').trigger('submit')
  await flushPromises()
  expect(again.sent).toEqual([{ body: 'Use the second.', question: false, reply_to: 77 }])
})

test('composer: nobody to ask on a standalone task with the CEO off', async () => {
  mountComposer({ t: ticket({ number: 3, type: 'task', story: null }) })
  expect(w!.get('[role="switch"], button[aria-checked]').attributes('disabled')).toBeDefined()
  expect(w!.text()).toContain('Nobody on this ticket can be asked')
})

test('composer: @ completion offers the roles with names; arrows and Enter choose; Escape closes', async () => {
  mountComposer()
  expect(w!.text()).toContain('@pm is quiet-heron')
  await type('Please @')
  const options = () => w!.findAll('[role="option"]').map((o) => o.text())
  expect(options()).toEqual(['@reviewer', '@pm quiet-heron', '@supervisor', '@user'])
  expect(box().attributes('aria-controls')).toBeTruthy()
  expect(box().attributes('aria-activedescendant')).toBeTruthy()
  await box().trigger('keydown', { key: 'ArrowDown' })
  await box().trigger('keydown', { key: 'Enter' })
  expect((box().element as HTMLTextAreaElement).value).toBe('Please @pm ')
  await type('and @u')
  expect(options()).toEqual(['@user'])
  await box().trigger('keydown', { key: 'Escape' })
  expect(options()).toEqual([])
  await type('mail a@')
  expect(options()).toEqual([])
})

test('composer: on a ticket with several projects @reviewer:<project id> is offered for each, and chosen as typed', async () => {
  const project = (id: string) => ({ project: id, base_branch: 'main', branch: `omo/${id}`, worktree: '', export_url: '', export_state: '' as const, export_error: '' })
  mountComposer({ t: ticket({ number: 9, type: 'story', projects: [project('api'), project('web')] }) })
  const options = () => w!.findAll('[role="option"]').map((o) => o.text())
  await type('Ask @rev')
  expect(options()).toEqual(['@reviewer', '@reviewer:api api', '@reviewer:web web'])
  await type('Ask @reviewer:')
  expect(options()).toEqual(['@reviewer:api api', '@reviewer:web web'])
  await type('Ask @reviewer:w')
  expect(options()).toEqual(['@reviewer:web web'])
  await box().trigger('keydown', { key: 'Enter' })
  expect((box().element as HTMLTextAreaElement).value).toBe('Ask @reviewer:web ')
  // A single project: nothing new.
  w!.unmount()
  mountComposer({ t: ticket({ number: 3, projects: [project('api')] }) })
  await type('@rev')
  expect(options()).toEqual(['@reviewer'])
})

test('composer: the unreachable note shows after sending', async () => {
  mountComposer({ send: async () => ['reviewer'] })
  await type('@reviewer look')
  await w!.get('form').trigger('submit')
  await flushPromises()
  expect(w!.get('[data-unreachable]').text()).toBe('Nobody works as @reviewer on #9 right now; the comment is stored.')
})

test('composer: a comment above 64 KiB is refused without a request and the text is kept', async () => {
  const { sent } = mountComposer()
  const long = 'é'.repeat(32 * 1024 + 1)
  await type(long)
  await w!.get('form').trigger('submit')
  await flushPromises()
  expect(sent).toHaveLength(0)
  expect(w!.text()).toContain('at most 64 KiB')
  expect((box().element as HTMLTextAreaElement).value).toBe(long)
  expect(box().attributes('aria-invalid')).toBe('true')
})

test('composer: the server refusing a long comment shows its message', async () => {
  mountComposer({
    send: async () => {
      throw new ApiError(422, 'validation', 'a comment has at most 64 KiB (65536 bytes); this one has 70000 bytes')
    },
  })
  await type('fine here')
  await w!.get('form').trigger('submit')
  await flushPromises()
  expect(w!.text()).toContain('this one has 70000 bytes')
  expect((box().element as HTMLTextAreaElement).value).toBe('fine here')
})

test('composer: "Add image…" uploads the chosen files, inserts their references and holds sending while one uploads', async () => {
  const id = '0f8fad5b-d9cb-469f-a165-70867728950e'
  let finish!: (i: Image) => void
  const names: string[] = []
  const { sent } = mountComposer({
    upload: (_file, name) => {
      names.push(name)
      return new Promise((r) => (finish = r))
    },
  })
  await type('Look:')
  const input = w!.get('[data-image-input]').element as HTMLInputElement
  Object.defineProperty(input, 'files', { value: [new File(['x'], 'shot.png', { type: 'image/png' })], configurable: true })
  await w!.get('[data-image-input]').trigger('change')
  expect(names).toEqual(['shot.png'])
  expect((box().element as HTMLTextAreaElement).value).toMatch(/Uploading shot\.png…/)
  expect(w!.get('[data-send]').attributes('disabled')).toBeDefined()
  expect(w!.text()).toContain('Uploading…')
  finish({
    id,
    ticket: 9,
    name: 'shot.png',
    content_type: 'image/png',
    size: 1,
    author: { role: 'user' },
    created_at: '2026-11-16T10:00:00Z',
    state: 'stored',
    path: '/x',
    markdown: `![shot.png](omo-image:${id})`,
  })
  await flushPromises()
  expect((box().element as HTMLTextAreaElement).value).toBe(`Look:\n![shot.png](omo-image:${id})\n`)
  await w!.get('form').trigger('submit')
  await flushPromises()
  expect(sent[0]!.body).toContain(`omo-image:${id}`)
})

test('composer: a refused image shows the server sentence and inserts nothing', async () => {
  mountComposer({ upload: () => Promise.reject(new ApiError(409, 'conflict', '#9 already has 100 images, the most a ticket keeps; the user can delete some first')) })
  await type('Text')
  const input = w!.get('[data-image-input]').element as HTMLInputElement
  Object.defineProperty(input, 'files', { value: [new File(['x'], 'one more.png', { type: 'image/png' })], configurable: true })
  await w!.get('[data-image-input]').trigger('change')
  await flushPromises()
  expect((box().element as HTMLTextAreaElement).value).toBe('Text')
  expect(w!.get('[data-image-errors]').text()).toBe('Not added: one more.png: #9 already has 100 images, the most a ticket keeps; the user can delete some first')
})

test('composer: without an upload function there is no image button', () => {
  mountComposer()
  expect(w!.find('[data-add-image]').exists()).toBe(false)
})

test('composer: Cmd+Enter sends as Ctrl+Enter does, and the hint names the key of the platform', async () => {
  const sent: CommentRequest[] = []
  mountComposer({ send: async (r) => (sent.push(r), []) })
  await type('From a Mac')
  await box().trigger('keydown', { key: 'Enter', metaKey: true })
  await flushPromises()
  expect(sent).toEqual([{ body: 'From a Mac', question: false }])
  expect(w!.get('.composer__hint').text()).toMatch(/^(Ctrl|Cmd)\+Enter sends\.$/)
})
