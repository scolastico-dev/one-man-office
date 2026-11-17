import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { ApiError } from '../../api/client'
import type { TicketSummary } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { ref } from 'vue'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'
import TicketPanel from './TicketPanel.vue'
import { ticketDrafts } from './drafts'
import { mountPanel, ok, panelHarness, refuse, type PanelHarness } from './panelTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

const demo = { project: 'demo', base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' }

async function open(hn: PanelHarness, n: number, onClose?: () => void) {
  const { wrapper, router } = await mountPanel(hn, TicketPanel, { number: n, ...(onClose ? { onClose } : {}) }, `/${n}`)
  mounted = wrapper
  await flushPromises()
  return { w: wrapper, router }
}

const actionLabels = (w: VueWrapper) => w.findAll('[data-action]').map((b) => b.text())
const action = (w: VueWrapper, id: string) => w.get(`[data-action="${id}"]`)

function seeded(...tickets: TicketSummary[]) {
  return (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
    omo.tickets = tickets
    omo.projects = [project('demo')]
  }
}

test('the head shows at once from the board; the description loads and is rendered', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4, title: 'Login page', projects: [demo] }), ticket({ number: 12, title: 'Other', column: 'ready' })))
  hn.descriptions.set(4, '## Goal\n\nSee #12 and <b>this</b>.')
  const { w } = await open(hn, 4)
  const panel = w.get('dialog[data-ticket-panel]')
  expect(panel.attributes('aria-labelledby')).toBe(w.get('[data-ticket-title]').attributes('id'))
  expect(w.get('[data-ticket-title]').text()).toBe('Login page')
  expect(w.get('[data-ticket-column]').text()).toBe('Planning')
  // The focus starts on Close, never on the editable title.
  await flushPromises()
  expect(document.activeElement).toBe(w.get('[data-close-panel]').element)
  const view = w.get('[data-description]')
  expect(view.find('h4').text()).toBe('Goal')
  expect(view.text()).toContain('<b>this</b>')
  expect(view.get('a[data-ticket-ref]').attributes('title')).toBe('#12 Other (Ready For Dev)')
})

test('changes from the stream update the open panel; a new description version replaces the shown one', async () => {
  const t = ticket({ number: 4, title: 'Old title', projects: [demo] })
  const hn = await panelHarness(seeded(t))
  hn.descriptions.set(4, 'first')
  const { w } = await open(hn, 4)
  hn.omo.change('ticket.updated', { ...t, title: 'New title', description_version: 2 })
  hn.omo.emit('description.changed', { ticket: 4, version: { number: 2, text: 'second', author: { role: 'pm' }, restored_from: null, created_at: '', current: true } }, 4)
  await flushPromises()
  expect(w.get('[data-ticket-title]').text()).toBe('New title')
  expect(w.get('[data-description]').text()).toBe('second')
  hn.omo.change('ticket.moved', { ...t, title: 'New title', column: 'ready', description_version: 2 })
  await flushPromises()
  expect(w.get('[data-ticket-column]').text()).toBe('Ready For Dev')
})

test('an unknown number says so and offers the way back', async () => {
  const hn = await panelHarness(seeded())
  let closed = 0
  const { w } = await open(hn, 99, () => closed++)
  expect(w.get('[data-ticket-missing]').text()).toContain('Ticket #99 does not exist.')
  await w.get('[data-ticket-missing] button').trigger('click')
  expect(closed).toBe(1)
})

test('an older Done ticket the board has not loaded is read but does not join the board', async () => {
  const hn = await panelHarness(seeded())
  hn.hidden.push(ticket({ number: 3, title: 'Old and done', column: 'done', resolution: 'completed', closed_at: '2026-01-01T00:00:00Z' }))
  const { w } = await open(hn, 3)
  expect(w.get('[data-ticket-title]').text()).toBe('Old and done')
  // Joining would leave a gap in the Done pages.
  expect(hn.session.store.view(3)).toBeUndefined()
  // Done tickets are read-only: no edit buttons, no actions.
  expect(w.find('[data-edit-title]').exists()).toBe(false)
  expect(w.find('[data-edit-description]').exists()).toBe(false)
  expect(actionLabels(w)).toEqual([])
})

test('a ticket of another column the board has not loaded is read and joins the board', async () => {
  const hn = await panelHarness(seeded())
  hn.hidden.push(ticket({ number: 3, title: 'Late arrival', column: 'ready', projects: [demo] }))
  const { w } = await open(hn, 3)
  expect(w.get('[data-ticket-title]').text()).toBe('Late arrival')
  expect(hn.session.store.view(3)?.title).toBe('Late arrival')
})

test('Approve shows in Planning and for a review waiting for the user only', async () => {
  const hn = await panelHarness(
    seeded(
      ticket({ number: 1, column: 'planning', projects: [demo] }),
      ticket({ number: 2, column: 'review', review_stage: 'human', projects: [{ ...demo, branch: 'omo/2' }] }),
      ticket({ number: 3, column: 'review', review_stage: 'agent', projects: [demo] }),
      ticket({ number: 4, column: 'ready', projects: [demo] }),
      ticket({ number: 5, column: 'review', review_stage: 'human', effective_when_approved: 'export', projects: [demo] }),
      ticket({ number: 6, column: 'planning', type: 'story', projects: [] }),
      ticket({ number: 7, column: 'planning', projects: [] }),
    ),
  )
  const labels = async (n: number) => {
    const { w } = await open(hn, n)
    const out = actionLabels(w)
    mounted?.unmount()
    mounted = null
    return out
  }
  expect(await labels(1)).toEqual(['Approve', 'Cancel ticket…'])
  expect(await labels(2)).toEqual(['Approve and merge…', 'Reject…', 'Cancel ticket…', 'Export…'])
  expect(await labels(3)).toEqual(['Cancel ticket…'])
  expect(await labels(4)).toEqual(['Cancel ticket…'])
  expect(await labels(5)).toEqual(['Approve and export…', 'Reject…', 'Cancel ticket…', 'Export…'])
  // A ticket the server would refuse to approve says why, and the reason can be fixed.
  const { w } = await open(hn, 6)
  expect(action(w, 'approve').attributes('disabled')).toBeDefined()
  expect(w.get('[data-action-why="approve"]').text()).toBe('Add a project to the story first.')
  mounted?.unmount()
  mounted = null
  // A task without a project needs none.
  expect(await labels(7)).toEqual(['Approve', 'Cancel ticket…'])
})

test('approving a review asks first and says whether it merges or exports; a refusal stays in the dialog', async () => {
  const t = ticket({ number: 2, column: 'review', review_stage: 'human', projects: [{ ...demo, branch: 'omo/2-login' }] })
  const hn = await panelHarness(seeded(t))
  const { w } = await open(hn, 2)
  await action(w, 'approve').trigger('click')
  expect(hn.sent).toHaveLength(0)
  const dlg = w.get('[data-approve-dialog]')
  expect(dlg.get('[data-approve-effect]').text()).toBe('omo merges omo/2-login into main locally.')
  hn.write.fn = () => refuse('invalid_transition', '#2 is already approved and merging', 409)
  await w.findAll('button').find((b) => b.text() === 'Approve and merge')!.trigger('click')
  await flushPromises()
  expect(hn.sent[0]).toMatchObject({ method: 'POST', path: '/api/tickets/2/move', body: { column: 'done', before: null } })
  expect(w.get('[data-approve-dialog] [data-action-error]').text()).toBe('#2 is already approved and merging')
})

test('Reject is offered for a review stuck at merging with a failed merge, next to Retry', async () => {
  const t = ticket({ number: 2, column: 'review', review_stage: 'merging', failed_step: 'merge', projects: [demo] })
  const hn = await panelHarness(seeded(t, ticket({ number: 3, column: 'review', review_stage: 'merging', projects: [demo] })))
  const { w } = await open(hn, 2)
  expect(actionLabels(w)).toEqual(['Retry merge', 'Reject…', 'Cancel ticket…'])
  expect(action(w, 'reject').attributes('disabled')).toBeUndefined()
  await action(w, 'reject').trigger('click')
  hn.write.fn = ok
  await w.get('[data-reject-comment]').setValue('The branch moved.')
  await w.get('#reject-form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]).toMatchObject({ path: '/api/tickets/2/reject', body: { comment: 'The branch moved.' } })
  mounted?.unmount()
  mounted = null
  const { w: other } = await open(hn, 3)
  expect(actionLabels(other)).toEqual(['Cancel ticket…'])
})

test('rejecting asks for a comment and sends it', async () => {
  const t = ticket({ number: 2, column: 'review', review_stage: 'human', projects: [demo] })
  const hn = await panelHarness(seeded(t))
  const { w } = await open(hn, 2)
  await action(w, 'reject').trigger('click')
  hn.write.fn = () => refuse('validation', 'a rejection needs a comment that says what to fix')
  await w.get('[data-reject-comment]').setValue('Tests are missing.')
  await w.get('#reject-form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]).toMatchObject({ path: '/api/tickets/2/reject', body: { comment: 'Tests are missing.' } })
  // The refusal shows in the dialog and the comment stays.
  expect(w.get('[data-move-error]').text()).toBe('a rejection needs a comment that says what to fix')
  expect((w.get('[data-reject-comment]').element as HTMLTextAreaElement).value).toBe('Tests are missing.')
})

test('Retry shows only with failed_step and is labelled with the step', async () => {
  const hn = await panelHarness(
    seeded(
      ticket({ number: 1, column: 'ready', failed_step: 'start', attention: 'The base branch main does not exist.', projects: [demo] }),
      ticket({ number: 2, column: 'in_progress', failed_step: 'agent', projects: [demo] }),
      ticket({ number: 3, column: 'review', review_stage: 'merging', failed_step: 'merge', projects: [demo] }),
      ticket({ number: 4, column: 'in_progress', failed_step: '', projects: [demo] }),
    ),
  )
  const { w } = await open(hn, 1)
  expect(actionLabels(w)).toEqual(['Retry start', 'Cancel ticket…'])
  // The reason is shown prominently.
  expect(w.get('[data-attention]').text()).toContain('Needs attention')
  expect(w.get('[data-attention]').text()).toContain('The base branch main does not exist.')
  // A refused retry shows the server's answer with the button.
  hn.write.fn = () => refuse('conflict', 'omo is starting work on, merging or exporting #1; try again in a moment.', 409)
  await action(w, 'retry').trigger('click')
  await flushPromises()
  expect(hn.sent[0]).toMatchObject({ method: 'POST', path: '/api/tickets/1/retry', body: {} })
  expect(w.get('[data-action-error]').text()).toBe('omo is starting work on, merging or exporting #1; try again in a moment.')
  hn.write.fn = () => {
    throw new ApiError(0, 'network', 'Cannot reach omo. Check that it is still running.')
  }
  await action(w, 'retry').trigger('click')
  await flushPromises()
  expect(w.get('[data-action-error]').text()).toContain('may have happened')
  w.unmount()
  for (const [n, label] of [
    [2, 'Retry agent'],
    [3, 'Retry merge'],
  ] as const) {
    const { w: x } = await open(hn, n)
    expect(actionLabels(x)[0]).toBe(label)
    x.unmount()
  }
  const { w: none } = await open(hn, 4)
  expect(actionLabels(none)).toEqual(['Cancel ticket…'])
})

test('Cancel and Retry are disabled with the busy reason while busy, also for a story with a busy task', async () => {
  const hn = await panelHarness(
    seeded(
      ticket({ number: 1, column: 'review', review_stage: 'merging', busy: true, failed_step: 'merge', projects: [demo] }),
      ticket({ number: 5, type: 'story', column: 'in_progress', projects: [demo] }),
      ticket({ number: 6, story: 5, column: 'ready', busy: true, projects: [demo] }),
    ),
  )
  const reason = (n: number) => `omo is starting work on, merging or exporting #${n}; try again in a moment.`
  const { w } = await open(hn, 1)
  expect(w.get('[data-busy]').text()).toBe('omo is working on this: merging…')
  for (const id of ['retry', 'cancel']) {
    expect(action(w, id).attributes('disabled')).toBeDefined()
    expect(w.get(`[data-action-why="${id}"]`).text()).toBe(reason(1))
  }
  w.unmount()
  const { w: story } = await open(hn, 5)
  expect(story.find('[data-busy]').exists()).toBe(true)
  expect(action(story, 'cancel').attributes('disabled')).toBeDefined()
  expect(story.get('[data-action-why="cancel"]').text()).toBe(reason(5))
})

test('Mark as merged shows on an exported ticket until it is marked; the pull requests are listed', async () => {
  const exported = ticket({
    number: 7,
    column: 'done',
    resolution: 'exported',
    projects: [{ ...demo, export_url: 'https://git.example/pr/7' }],
  })
  const hn = await panelHarness(seeded(exported, ticket({ number: 8, column: 'done', resolution: 'exported', export_merged: true })))
  const { w } = await open(hn, 7)
  expect(actionLabels(w)).toEqual(['Mark as merged'])
  const pr = w.get('[data-pull-requests] a')
  expect(pr.attributes('href')).toBe('https://git.example/pr/7')
  expect(pr.attributes('rel')).toBe('noreferrer noopener')
  hn.write.fn = () => ok({}, 50)
  await action(w, 'mark-merged').trigger('click')
  await flushPromises()
  expect(hn.sent[0]).toEqual({ method: 'POST', path: '/api/tickets/7/mark-merged', body: undefined })
  w.unmount()
  const { w: done } = await open(hn, 8)
  expect(actionLabels(done)).toEqual([])
})

test('the title is edited in place: Enter saves only the title, Escape cancels, limits are checked first', async () => {
  const t = ticket({ number: 4, title: 'Old', projects: [demo] })
  const hn = await panelHarness(seeded(t))
  let closed = 0
  const { w } = await open(hn, 4, () => closed++)
  await w.get('[data-edit-title]').trigger('click')
  const input = w.get('[data-title-input]')
  // A field that wraps, so a long title shows in full on a phone.
  expect(input.element.tagName).toBe('TEXTAREA')
  await input.setValue('x'.repeat(201))
  await input.trigger('keydown', { key: 'Enter' })
  expect(w.get('[data-title-error]').text()).toBe('The title has 201 characters; at most 200 fit.')
  await input.setValue('Tab\there')
  await input.trigger('keydown', { key: 'Enter' })
  expect(w.get('[data-title-error]').text()).toBe('The title must be one line without control characters.')
  await input.setValue('Soft\u00ADhyphen')
  await input.trigger('keydown', { key: 'Enter' })
  expect(w.get('[data-title-error]').text()).toBe('The title must be one line without control characters.')
  expect(hn.sent).toHaveLength(0)

  // Escape leaves the title and does not close the panel.
  await input.trigger('keydown', { key: 'Escape' })
  expect(w.find('[data-title-input]').exists()).toBe(false)
  expect(closed).toBe(0)

  await w.get('[data-edit-title]').trigger('click')
  hn.write.fn = () => ok({ ...t, title: 'New', description: '' }, 77)
  await w.get('[data-title-input]').setValue('  New  ')
  await w.get('[data-title-input]').trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(hn.sent[0]).toEqual({ method: 'PATCH', path: '/api/tickets/4', body: { title: 'New' } })
  // The saved title shows until the stream brings it; the store is not written from the answer.
  expect(w.get('[data-ticket-title]').text()).toBe('New')
  expect(hn.session.store.view(4)?.title).toBe('Old')
})

test('editing the title again right after saving, before the stream delivers it, starts from the saved title', async () => {
  const t = ticket({ number: 4, title: 'Old', projects: [demo] })
  const hn = await panelHarness(seeded(t))
  const { w } = await open(hn, 4)
  await w.get('[data-edit-title]').trigger('click')
  hn.write.fn = () => ok({ ...t, title: 'New', description: '' }, 77)
  await w.get('[data-title-input]').setValue('New')
  await w.get('[data-title-input]').trigger('keydown', { key: 'Enter' })
  await flushPromises()
  await w.get('[data-edit-title]').trigger('click')
  expect((w.get('[data-title-input]').element as HTMLTextAreaElement).value).toBe('New')
  expect(w.find('[data-title-changed]').exists()).toBe(false)
  // The stream delivers the own change: still nothing changed meanwhile.
  hn.omo.change('ticket.updated', { ...t, title: 'New' })
  await flushPromises()
  expect(w.find('[data-title-changed]').exists()).toBe(false)
  expect((w.get('[data-title-input]').element as HTMLTextAreaElement).value).toBe('New')
})

test('the description editor adds a chosen image at the caret and saves its reference; a refusal inserts nothing', async () => {
  const t = ticket({ number: 4, projects: [demo], description_version: 1 })
  const hn = await panelHarness(seeded(t))
  hn.descriptions.set(4, 'Before')
  const { w } = await open(hn, 4)
  await w.get('[data-edit-description]').trigger('click')
  const area = () => w.get('[data-description-editor] textarea')
  const id = '0f8fad5b-d9cb-469f-a165-70867728950e'
  const choose = async (name: string) => {
    const input = w.get('[data-description-editor] [data-image-input]').element as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [new File(['xyz'], name, { type: 'image/png' })], configurable: true })
    await w.get('[data-description-editor] [data-image-input]').trigger('change')
    await flushPromises()
  }
  hn.write.fn = (s) =>
    s.method === 'UPLOAD'
      ? ok({ id, ticket: 4, name: 'a b.png', markdown: `![a b.png](omo-image:${id})` }, 7)
      : ok({ number: 2, text: '', author: { role: 'user' }, restored_from: null, created_at: '', current: true }, 8)
  await choose('a b.png')
  expect(hn.sent[0]).toEqual({ method: 'UPLOAD', path: '/api/tickets/4/images', body: { size: 3, headers: { 'Omo-Image-Name': 'a%20b.png' } } })
  expect((area().element as HTMLTextAreaElement).value).toBe(`Before\n![a b.png](omo-image:${id})\n`)

  hn.write.fn = () => refuse('too_large', 'an image has at most 10 MiB', 413)
  await choose('big.png')
  expect((area().element as HTMLTextAreaElement).value).toBe(`Before\n![a b.png](omo-image:${id})\n`)
  expect(w.get('[data-description-editor] [data-image-errors]').text()).toBe('Not added: big.png: an image has at most 10 MiB')

  hn.write.fn = () => ok({ number: 2, text: '', author: { role: 'user' }, restored_from: null, created_at: '', current: true }, 8)
  await w.get('[data-description-editor]').trigger('submit')
  await flushPromises()
  expect(hn.sent.at(-1)).toEqual({ method: 'PUT', path: '/api/tickets/4/description', body: { text: `Before\n![a b.png](omo-image:${id})\n`, base_version: 1 } })
})

test('a stale description keeps my text and offers both versions', async () => {
  const t = ticket({ number: 4, projects: [demo], description_version: 3 })
  const hn = await panelHarness(seeded(t))
  hn.descriptions.set(4, 'base text')
  const { w } = await open(hn, 4)
  await w.get('[data-edit-description]').trigger('click')
  const area = () => w.get('[data-description-editor] textarea')
  await area().setValue('my text')

  // Someone else saves meanwhile; the server refuses the edit based on version 3.
  hn.readTicket.fn = async () => ok({ ...t, description_version: 4, description: 'their text' })
  hn.write.fn = () => refuse('stale_description', 'the description of #4 changed', 409, { current_version: 4 })
  await w.get('[data-description-editor]').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]).toEqual({ method: 'PUT', path: '/api/tickets/4/description', body: { text: 'my text', base_version: 3 } })
  expect(w.get('[data-stale]').text()).toContain('Someone changed the description meanwhile.')
  expect((area().element as HTMLTextAreaElement).value).toBe('my text')

  await w.get('[data-show-theirs]').trigger('click')
  expect(w.get('[data-theirs]').text()).toContain('their text')

  hn.write.fn = () => ok({ number: 5, text: 'my text', author: { role: 'user' }, restored_from: null, created_at: '', current: true }, 90)
  await w.get('[data-save-anyway]').trigger('click')
  await flushPromises()
  expect(hn.sent[1]).toEqual({ method: 'PUT', path: '/api/tickets/4/description', body: { text: 'my text', base_version: 4 } })
  expect(w.find('[data-description-editor]').exists()).toBe(false)
  expect(w.get('[data-description]').text()).toBe('my text')
})

test('editing again right after saving, before the stream delivers it, starts from the saved text and version', async () => {
  const t = ticket({ number: 4, projects: [demo], description_version: 3 })
  const hn = await panelHarness(seeded(t))
  hn.descriptions.set(4, 'base text')
  const { w } = await open(hn, 4)
  const area = () => w.get('[data-description-editor] textarea')
  hn.write.fn = () => ok({ number: 4, text: 'first', author: { role: 'user' }, restored_from: null, created_at: '', current: true }, 90)
  await w.get('[data-edit-description]').trigger('click')
  await area().setValue('first')
  await w.get('[data-description-editor]').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ text: 'first', base_version: 3 })

  // The stream has not delivered version 4 yet.
  await w.get('[data-edit-description]').trigger('click')
  expect((area().element as HTMLTextAreaElement).value).toBe('first')
  expect(w.find('[data-stale]').exists()).toBe(false)
  hn.write.fn = () => ok({ number: 5, text: 'second', author: { role: 'user' }, restored_from: null, created_at: '', current: true }, 91)
  await area().setValue('second')
  await w.get('[data-description-editor]').trigger('submit')
  await flushPromises()
  expect(hn.sent[1]?.body).toEqual({ text: 'second', base_version: 4 })
})

test('a newer version arriving while I edit is never saved over without asking', async () => {
  const t = ticket({ number: 4, projects: [demo], description_version: 1 })
  const hn = await panelHarness(seeded(t))
  hn.descriptions.set(4, 'v1')
  const { w } = await open(hn, 4)
  await w.get('[data-edit-description]').trigger('click')
  await w.get('[data-description-editor] textarea').setValue('mine')
  hn.omo.emit('description.changed', { ticket: 4, version: { number: 2, text: 'v2', author: { role: 'developer' }, restored_from: null, created_at: '', current: true } }, 4)
  await flushPromises()
  // My text stays; the newer version is announced.
  expect((w.get('[data-description-editor] textarea').element as HTMLTextAreaElement).value).toBe('mine')
  expect(w.find('[data-stale]').exists()).toBe(true)
  await w.get('[data-description-editor]').trigger('submit')
  await flushPromises()
  expect(hn.sent).toHaveLength(0)
})

test("editing tells when the ticket's agent is told; Escape with unsaved text asks before closing", async () => {
  const t = ticket({
    number: 4,
    column: 'in_progress',
    projects: [demo],
    agents: [{ session: 's1', role: 'developer', name: 'dev-1', state: 'running', purpose: 'work' }],
  })
  const hn = await panelHarness(seeded(t))
  let closed = 0
  const { w } = await open(hn, 4, () => closed++)
  await w.get('[data-edit-description]').trigger('click')
  expect(w.get('[data-agent-told]').text()).toBe('The developer is told about the change.')

  // Without changes Escape closes at once; with changes it asks.
  await w.get('[data-description-editor] textarea').setValue('changed')
  await w.get('dialog[data-ticket-panel]').trigger('keydown', { key: 'Escape' })
  expect(closed).toBe(0)
  expect(w.get('[data-unsaved]').text()).toContain('You have unsaved changes.')
  await w.get('[data-keep-editing]').trigger('click')
  expect(w.find('[data-unsaved]').exists()).toBe(false)
  await w.get('dialog[data-ticket-panel]').trigger('keydown', { key: 'Escape' })
  await w.get('[data-discard]').trigger('click')
  expect(closed).toBe(1)
})

test('leaving the address with unsaved text asks first', async () => {
  const t = ticket({ number: 4, projects: [demo] })
  const hn = await panelHarness(seeded(t))
  const { w, router } = await open(hn, 4)
  await w.get('[data-edit-title]').trigger('click')
  await w.get('[data-title-input]').setValue('Unsaved')
  await router.push('/')
  expect(router.currentRoute.value.path).toBe('/4')
  expect(w.find('[data-unsaved]').exists()).toBe(true)
  await w.get('[data-discard]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/')
})

test('a reload or a closed tab asks first while a comment is typed but not sent, as for an unsaved edit', async () => {
  ticketDrafts.clear()
  const t = ticket({ number: 4, projects: [demo] })
  const hn = await panelHarness(seeded(t))
  await open(hn, 4)
  const unload = () => {
    const e = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(e)
    return e.defaultPrevented
  }
  expect(unload()).toBe(false)
  ticketDrafts.setComment(4, 'Half an answer')
  expect(unload()).toBe(true)
  // A draft on another ticket is not this view's.
  ticketDrafts.setComment(4, '')
  ticketDrafts.setComment(5, 'Elsewhere')
  expect(unload()).toBe(false)
  ticketDrafts.clear()
})

test('a description is rendered without raw HTML and with safe links', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4 })))
  hn.descriptions.set(4, '<img src=x onerror="alert(1)"> [x](javascript:alert(1)) ![p](https://evil.example/p.png) https://ok.example')
  const { w } = await open(hn, 4)
  const view = w.get('[data-description]')
  expect(view.find('img').exists()).toBe(false)
  expect(view.findAll('a').map((a) => a.attributes('href'))).toEqual(['https://evil.example/p.png', 'https://ok.example'])
})

test('under the lock the panel steps aside without closing, and comes back after', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 3, column: 'in_progress' })))
  const suspended = ref(false)
  let closed = 0
  const { wrapper } = await mountPanel(hn, TicketPanel, { number: 3, onClose: () => closed++ }, '/3', { [BOARD_SUSPENDED as symbol]: suspended })
  mounted = wrapper
  await flushPromises()
  const el = wrapper.get<HTMLDialogElement>('dialog[data-ticket-panel]').element
  expect(el.open).toBe(true)
  suspended.value = true
  await flushPromises()
  expect(el.open).toBe(false)
  expect(closed).toBe(0)
  suspended.value = false
  await flushPromises()
  expect(el.open).toBe(true)
  expect(closed).toBe(0)
})

/** A window `wide` or as narrow as a phone, whose width can change later. */
function stubWidth(narrow: boolean) {
  const listeners = new Set<() => void>()
  const state = { matches: narrow }
  vi.stubGlobal('matchMedia', (q: string) => ({
    get matches() {
      return q.includes('max-width: 767.98px') ? state.matches : false
    },
    media: q,
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
  }))
  return (n: boolean) => {
    state.matches = n
    for (const fn of listeners) fn()
  }
}

test('on a wide window the panel is not modal: shown in place, no aria-modal, nothing else made inert', async () => {
  stubWidth(false)
  const show = vi.spyOn(HTMLDialogElement.prototype, 'show')
  const showModal = vi.spyOn(HTMLDialogElement.prototype, 'showModal')
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] })))
  const { w } = await open(hn, 4)
  const panel = w.get('dialog[data-ticket-panel]')
  expect(show).toHaveBeenCalledTimes(1)
  expect(showModal).not.toHaveBeenCalled()
  expect(panel.attributes('aria-modal')).toBeUndefined()
  expect(w.get('[data-ticket-area]').classes()).not.toContain('tpanel-area--modal')
  expect(document.activeElement).toBe(w.get('[data-close-panel]').element)
})

test('on a phone the panel fills the screen as a modal dialog, and crossing the width shows it the other way, open all along', async () => {
  const setNarrow = stubWidth(true)
  const showModal = vi.spyOn(HTMLDialogElement.prototype, 'showModal')
  const show = vi.spyOn(HTMLDialogElement.prototype, 'show')
  let closed = 0
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] })))
  const { w } = await open(hn, 4, () => closed++)
  const panel = w.get<HTMLDialogElement>('dialog[data-ticket-panel]')
  expect(showModal).toHaveBeenCalledTimes(1)
  expect(panel.attributes('aria-modal')).toBe('true')
  setNarrow(false)
  await flushPromises()
  expect(show).toHaveBeenCalledTimes(1)
  expect(panel.element.open).toBe(true)
  expect(panel.attributes('aria-modal')).toBeUndefined()
  // The close of the modal one is not taken for a request to close the ticket.
  await new Promise((r) => setTimeout(r, 0))
  expect(closed).toBe(0)
})

test('Escape inside the non-modal panel closes it; Escape that something inside took does not', async () => {
  stubWidth(false)
  let closed = 0
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo] })))
  const { w } = await open(hn, 4, () => closed++)
  const ev = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true })
  ev.preventDefault()
  w.get('[data-close-panel]').element.dispatchEvent(ev)
  expect(closed).toBe(0)
  await w.get('[data-close-panel]').trigger('keydown', { key: 'Escape' })
  expect(closed).toBe(1)
})

test('a task without a project: approving says it is done with nothing to merge, no Export, the cancel keeps its folder', async () => {
  const t = ticket({ number: 3, column: 'review', review_stage: 'human', projects: [], workdir: '/w/3', effective_when_approved: 'export' })
  const hn = await panelHarness(seeded(t))
  const { w } = await open(hn, 3)
  expect(actionLabels(w)).toContain('Approve…')
  expect(w.find('[data-action="export"]').exists()).toBe(false)
  await action(w, 'approve').trigger('click')
  const dlg = w.get('[data-approve-dialog]')
  expect(dlg.get('[data-approve-effect]').text()).toContain('nothing to merge or export')
  expect(dlg.text()).not.toContain('merging…')
  expect(w.find('[data-merge-instead]').exists()).toBe(false)
  hn.write.fn = () => ok({})
  await w.findAll('button').find((b) => b.text() === 'Approve')!.trigger('click')
  await flushPromises()
  expect(hn.sent[0]).toMatchObject({ method: 'POST', path: '/api/tickets/3/move', body: { column: 'done', before: null } })
  // The dialog closed once omo accepted it.
  expect(w.find('[data-approve-dialog]').exists()).toBe(false)

  await action(w, 'cancel').trigger('click')
  const cancel = w.get('[data-cancel-dialog]')
  expect(cancel.find('input[value="delete"]').exists()).toBe(false)
  expect(cancel.get('[data-cancel-effect]').text()).toContain('Its working folder /w/3 stays until you remove it.')
})

test('the working folder of a done task without a project is removed after a confirmation', async () => {
  const t = ticket({ number: 5, column: 'done', resolution: 'completed', projects: [], workdir: '/w/5' })
  const hn = await panelHarness(seeded(t))
  const { w } = await open(hn, 5)
  await w.get('[data-remove-workdir]').trigger('click')
  const dlg = w.get('[data-remove-workdir-dialog]')
  expect(dlg.text()).toContain('/w/5')
  expect(hn.sent).toHaveLength(0)
  hn.write.fn = () => refuse('conflict', 'a terminal is open in the working folder /w/5; close it, then remove the folder again', 409)
  await w.get('#remove-workdir-form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]).toEqual({ method: 'POST', path: '/api/tickets/5/remove-workdir', body: undefined })
  expect(w.get('[data-remove-workdir-dialog] [data-action-error]').text()).toContain('a terminal is open')
})

test('the attention heading is Needs attention for the user and names the supervisor otherwise', async () => {
  const hn = await panelHarness(
    seeded(
      ticket({ number: 1, column: 'in_progress', failed_step: 'agent', attention: 'The agent keeps failing.', projects: [demo] }),
      ticket({ number: 2, column: 'in_progress', failed_step: 'agent', attention: 'The agent keeps failing.', supervisor: 'ceo', projects: [demo] }),
    ),
  )
  const { w } = await open(hn, 1)
  expect(w.get('[data-attention] .actions__attention-head').text()).toBe('Needs attention')
  const { w: other } = await open(hn, 2)
  expect(other.get('[data-attention] .actions__attention-head').text()).toBe('Needs the CEO')
})

test('the Approve hint of a task says it waits while its story is not In Progress', async () => {
  const hn = await panelHarness(
    seeded(
      ticket({ number: 1, type: 'story', column: 'planning', projects: [demo] }),
      ticket({ number: 2, story: 1, column: 'planning', projects: [demo] }),
      ticket({ number: 3, column: 'planning', projects: [demo] }),
    ),
  )
  const { w } = await open(hn, 2)
  expect(action(w, 'approve').attributes('title')).toContain('waits there until its story #1 is In Progress')
  const { w: standalone } = await open(hn, 3)
  expect(action(standalone, 'approve').attributes('title')).toContain('an agent starts on it as soon as nothing holds it back')
})
