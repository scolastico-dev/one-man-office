import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { Model, TicketSummary } from '../../api/types'
import { general, project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { mountPanel, ok, panelHarness, refuse, type PanelHarness } from './panelTesting'
import TicketFields from './TicketFields.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  general.ceo.enabled = false
})

const demo = { project: 'demo', base_branch: 'main', branch: '', worktree: '', export_url: '' }
const other = { project: 'other', base_branch: 'main', branch: '', worktree: '', export_url: '' }
const models: Model[] = [
  { id: 'claude/sonnet', profile: 'claude', name: 'sonnet', cli: 'claude', description: 'Fast', selectable: true },
  { id: 'claude/opus', profile: 'claude', name: 'opus', cli: 'claude', description: '', selectable: true },
  { id: 'claude/hidden', profile: 'claude', name: 'hidden', cli: 'claude', description: '', selectable: false },
]

function seeded(...tickets: TicketSummary[]) {
  return (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
    omo.tickets = tickets
    omo.projects = [project('demo'), project('other')]
  }
}

async function open(hn: PanelHarness, n: number) {
  const client = hn.session.client
  const get = client.get.bind(client)
  client.get = ((path: string, q?: never) => (path === '/api/models' ? Promise.resolve(ok(models)) : get(path, q))) as typeof client.get
  const { wrapper, router } = await mountPanel(hn, TicketFields, { ticket: hn.session.store.view(n) }, `/${n}`)
  mounted = wrapper
  await flushPromises()
  return { w: wrapper, router }
}

const row = (w: VueWrapper, field: string) => w.get(`[data-field="${field}"]`)
const opener = (w: VueWrapper, field: string) => row(w, field).get('[data-edit-open]')
async function choose(w: VueWrapper, field: string, value: string) {
  await opener(w, field).trigger('click')
  await row(w, field).get('select').setValue(value)
  await row(w, field).get('form').trigger('submit')
}

/** Mounts with a ticket the test can later replace, as the stream does. */
async function reactive(t: TicketSummary) {
  const hn = await panelHarness(seeded(t))
  const client = hn.session.client
  const get = client.get.bind(client)
  client.get = ((path: string, q?: never) => (path === '/api/models' ? Promise.resolve(ok(models)) : get(path, q))) as typeof client.get
  const { defineComponent, h, computed } = await import('vue')
  const Host = defineComponent({
    setup() {
      const t = computed(() => hn.session.store.view(1)!)
      return () => h(TicketFields, { ticket: t.value })
    },
  })
  const { wrapper } = await mountPanel(hn, Host, {}, '/1')
  mounted = wrapper
  await flushPromises()
  return { hn, w: wrapper }
}

test('saving a field sends only that key; Default sends null', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, projects: [demo], model: 'claude/sonnet', review: 'human', effective_review: 'human' }))
  hn.write.fn = () => ok({})
  await choose(w, 'model', 'claude/opus')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'PATCH', path: '/api/tickets/1', body: { model: 'claude/opus' } }])
  await choose(w, 'review', '')
  await flushPromises()
  expect(hn.sent[1]?.body).toEqual({ review: null })
  await choose(w, 'model', '')
  await flushPromises()
  expect(hn.sent[2]?.body).toEqual({ model: null })
  expect(hn.sent).toHaveLength(3)
})

test('the options show defaults with their value and every model, marking those agents cannot choose', async () => {
  const { w } = await reactive(ticket({ number: 1, projects: [demo] }))
  expect(row(w, 'review').text()).toContain('Default (Agent)')
  expect(row(w, 'when_approved').text()).toContain('Default (Merge locally)')
  await opener(w, 'model').trigger('click')
  const options = row(w, 'model').findAll('option').map((o) => o.text())
  expect(options).toEqual(["Default (the role's model)", 'claude/sonnet', 'claude/opus', 'claude/hidden (not selectable by agents)'])
})

test('the saved value shows until the stream delivers it, then the stream wins', async () => {
  const t = ticket({ number: 1, projects: [demo], review: null, effective_review: 'agent' })
  const { hn, w } = await reactive(t)
  hn.write.fn = () => ok({}, hn.omo.eventId + 1)
  await choose(w, 'review', 'human')
  await flushPromises()
  expect(row(w, 'review').text()).toContain('Human')
  expect(row(w, 'review').find('select').exists()).toBe(false)
  // The stream says something else (another editor was faster): its value wins.
  hn.omo.change('ticket.updated', { ...t, review: 'skipped', effective_review: 'skipped' })
  await flushPromises()
  expect(row(w, 'review').text()).toContain('Skipped')
})

test('a refusal shows the server message at the field and restores the previous value', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, projects: [demo], model: 'claude/sonnet' }))
  hn.write.fn = () => refuse('validation', 'model "claude/opus" is not selectable by agents')
  await opener(w, 'model').trigger('click')
  await row(w, 'model').get('select').setValue('claude/opus')
  // Enter in the list saves, as Enter in a text field does.
  await row(w, 'model').get('select').trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(row(w, 'model').get('[data-field-error]').text()).toBe('model "claude/opus" is not selectable by agents')
  expect(row(w, 'model').get('[data-edit-open]').text()).toBe('claude/sonnet')
})

test('the control is disabled while the request runs', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, projects: [demo] }))
  let release: () => void = () => {}
  hn.write.fn = () => new Promise((res) => (release = () => res(ok({}))))
  await opener(w, 'model').trigger('click')
  await row(w, 'model').get('select').setValue('claude/opus')
  await row(w, 'model').get('form').trigger('submit')
  expect(row(w, 'model').get('fieldset').attributes('disabled')).toBeDefined()
  expect(row(w, 'model').get('[data-save]').attributes('disabled')).toBeDefined()
  release()
  await flushPromises()
  expect(row(w, 'model').find('select').exists()).toBe(false)
})

test('a change from elsewhere while editing is announced and does not overwrite the draft', async () => {
  const t = ticket({ number: 1, projects: [demo], review: null })
  const { hn, w } = await reactive(t)
  await opener(w, 'review').trigger('click')
  await row(w, 'review').get('select').setValue('human')
  hn.omo.change('ticket.updated', { ...t, review: 'skipped', effective_review: 'skipped' })
  await flushPromises()
  expect(row(w, 'review').get('[data-changed]').text()).toContain('It is now Skipped.')
  expect((row(w, 'review').get('select').element as HTMLSelectElement).value).toBe('human')
  await row(w, 'review').get('[data-use-new]').trigger('click')
  expect((row(w, 'review').get('select').element as HTMLSelectElement).value).toBe('skipped')
  expect(row(w, 'review').find('[data-changed]').exists()).toBe(false)
})

test('keeping my own value after a change from elsewhere still saves it', async () => {
  const t = ticket({ number: 1, projects: [demo], review: null })
  const { hn, w } = await reactive(t)
  hn.write.fn = () => ok({})
  await opener(w, 'review').trigger('click')
  hn.omo.change('ticket.updated', { ...t, review: 'skipped', effective_review: 'skipped' })
  await flushPromises()
  expect(row(w, 'review').get('[data-changed]').text()).toContain('It is now Skipped.')
  // The draft is still what the edit began with (the default); saving it must not be dropped as "unchanged".
  await row(w, 'review').get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'PATCH', path: '/api/tickets/1', body: { review: null } }])
})

test('a draft equal to the value now on the board sends nothing', async () => {
  const t = ticket({ number: 1, projects: [demo], review: null })
  const { hn, w } = await reactive(t)
  await opener(w, 'review').trigger('click')
  await row(w, 'review').get('select').setValue('human')
  hn.omo.change('ticket.updated', { ...t, review: 'human', effective_review: 'human' })
  await flushPromises()
  await row(w, 'review').get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent).toHaveLength(0)
  expect(row(w, 'review').find('select').exists()).toBe(false)
})

test('a lock that appears under an open edit disables it and says why', async () => {
  const t = ticket({ number: 1, projects: [demo], column: 'planning' })
  const { hn, w } = await reactive(t)
  hn.write.fn = () => ok({})
  await opener(w, 'projects').trigger('click')
  expect(row(w, 'projects').find('[data-locked]').exists()).toBe(false)
  hn.omo.change('ticket.updated', { ...t, projects: [{ ...demo, branch: 'omo/1' }] })
  await flushPromises()
  const r = row(w, 'projects')
  expect(r.get('[data-locked]').text()).toBe('Locked while the branch omo/1 exists.')
  expect(r.get('fieldset').attributes('disabled')).toBeDefined()
  expect(r.get('[data-save]').attributes('disabled')).toBeDefined()
  await r.get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent).toHaveLength(0)
  // Cancel still works.
  await r.get('[data-cancel]').trigger('click')
  expect(row(w, 'projects').find('[data-edit-form]').exists()).toBe(false)
})

test('Escape cancels an edit without sending anything and is not passed on', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, projects: [demo] }))
  await opener(w, 'model').trigger('click')
  await row(w, 'model').get('select').setValue('claude/opus')
  let bubbled = false
  document.addEventListener('keydown', () => (bubbled = true), { once: true })
  await row(w, 'model').get('select').trigger('keydown', { key: 'Escape', bubbles: true })
  expect(row(w, 'model').find('select').exists()).toBe(false)
  expect(hn.sent).toHaveLength(0)
  expect(bubbled).toBe(false)
})

test('a locked field says why: the branch, omo working, a done ticket', async () => {
  const branched = { ...demo, branch: 'omo/1' }
  const { w } = await reactive(ticket({ number: 1, projects: [branched], column: 'planning' }))
  expect(row(w, 'projects').get('[data-locked]').text()).toBe('Locked while the branch omo/1 exists.')
  expect(row(w, 'base_branch').get('[data-locked]').text()).toBe('Locked while the branch omo/1 exists.')
  expect(row(w, 'projects').find('[data-edit-open]').exists()).toBe(false)
  expect(row(w, 'model').find('[data-edit-open]').exists()).toBe(true)
  mounted?.unmount()

  const busy = await reactive(ticket({ number: 1, projects: [demo], column: 'ready', busy: true }))
  expect(row(busy.w, 'projects').get('[data-locked]').text()).toContain('omo is working on this ticket')
  expect(busy.w.get('[data-add-dependency] [data-locked]').text()).toContain('omo is working on this ticket')
  mounted?.unmount()

  const done = await reactive(ticket({ number: 1, projects: [demo], column: 'done', resolution: 'completed' }))
  expect(row(done.w, 'model').get('[data-locked]').text()).toContain('done')
  expect(done.w.findAll('[data-edit-open]')).toHaveLength(0)
})

test('shows the fields that belong to the ticket: no when-approved in a story, story fields on a story', async () => {
  const inStory = await reactive(ticket({ number: 1, story: 9, projects: [demo] }))
  expect(inStory.w.find('[data-field="when_approved"]').exists()).toBe(false)
  expect(inStory.w.find('[data-field="base_branch"]').exists()).toBe(false)
  expect(inStory.w.find('[data-field="story"]').exists()).toBe(true)
  mounted?.unmount()
  const story = await reactive(ticket({ number: 1, type: 'story', projects: [demo] }))
  expect(story.w.find('[data-field="task_release"]').exists()).toBe(true)
  expect(story.w.find('[data-field="max_parallel"]').exists()).toBe(true)
  expect(story.w.find('[data-field="story"]').exists()).toBe(false)
})

test('review Human or Skipped on a story names the missing full test suite', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, type: 'story', projects: [demo], review: 'agent', effective_review: 'agent' }))
  expect(row(w, 'review').find('[data-full-suite-note]').exists()).toBe(false)
  await opener(w, 'review').trigger('click')
  await row(w, 'review').get('select').setValue('human')
  expect(row(w, 'review').get('[data-full-suite-note]').text()).toBe('No agent runs the full test suite for this story.')
  hn.write.fn = () => ok({}, hn.omo.eventId + 1)
  await row(w, 'review').get('form').trigger('submit')
  await flushPromises()
  expect(row(w, 'review').find('select').exists()).toBe(false)
  expect(row(w, 'review').find('[data-full-suite-note]').exists()).toBe(true)
})

test('tasks at once: empty sends null, a bad number is not sent', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, type: 'story', projects: [demo], max_parallel: 2 }))
  hn.write.fn = () => ok({})
  await opener(w, 'max_parallel').trigger('click')
  await row(w, 'max_parallel').get('input').setValue('zero')
  await row(w, 'max_parallel').get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent).toHaveLength(0)
  expect(row(w, 'max_parallel').get('[data-field-error]').text()).toContain('whole number')
  await row(w, 'max_parallel').get('input').setValue('')
  await row(w, 'max_parallel').get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ max_parallel: null })
})

test('tasks at once has no upper limit of its own: the server only requires 1 or more', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, type: 'story', projects: [demo], max_parallel: 2 }))
  hn.write.fn = () => ok({})
  await opener(w, 'max_parallel').trigger('click')
  await row(w, 'max_parallel').get('input').setValue('10000')
  await row(w, 'max_parallel').get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ max_parallel: 10000 })
})

test('the user may set a model agents cannot choose', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, projects: [demo] }))
  hn.write.fn = () => ok({})
  await choose(w, 'model', 'claude/hidden')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ model: 'claude/hidden' })
})

test('the base branches send only the one that changed', async () => {
  const { hn, w } = await reactive(ticket({ number: 1, type: 'story', projects: [demo, other] }))
  hn.write.fn = () => ok({})
  await opener(w, 'base_branch').trigger('click')
  const inputs = row(w, 'base_branch').findAll('input')
  await inputs[1]!.setValue('develop')
  await row(w, 'base_branch').get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ base_branches: { other: 'develop' } })
})

const withStories = [
  ticket({ number: 1, type: 'story', title: 'Story one', projects: [demo] }),
  ticket({ number: 2, type: 'story', title: 'Story two', projects: [other] }),
  ticket({ number: 3, title: 'The task', projects: [demo] }),
  ticket({ number: 4, title: 'Task four', projects: [demo] }),
  ticket({ number: 5, type: 'story', title: 'Finished story', projects: [demo], column: 'done', resolution: 'completed' }),
]

async function mountTask(n = 3) {
  const hn = await panelHarness(seeded(...withStories))
  const { w } = await open(hn, n)
  return { hn, w }
}

test('moving a task into a story says what changes first and sends story only when the project fits', async () => {
  const { hn, w } = await mountTask()
  hn.write.fn = () => ok({})
  await opener(w, 'story').trigger('click')
  const f = row(w, 'story')
  expect(f.get('[data-move-note]').text()).toContain('supervisor to the new default')
  expect(f.get('[data-move-note]').text()).toContain("one of the story's projects")
  // Only stories that take tasks are offered.
  expect(f.findAll('option').map((o) => o.text())).toEqual(['No story: a standalone task', '#1 Story one (Planning)', '#2 Story two (Planning)'])
  await f.get('select').setValue('1')
  expect(f.find('fieldset.picker').exists()).toBe(false)
  await f.get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ story: 1 })
})

test('a story that does not use the task project asks for one of its own and sends both', async () => {
  const { hn, w } = await mountTask()
  hn.write.fn = () => ok({})
  await opener(w, 'story').trigger('click')
  const f = row(w, 'story')
  await f.get('select').setValue('2')
  expect(f.get('fieldset.picker').text()).toContain('is not one of #2')
  await f.get('form').trigger('submit')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ story: 2, projects: ['other'] })
})

test('taking a task out of its story sends story null', async () => {
  const hn = await panelHarness(seeded(...withStories.slice(0, 2), ticket({ number: 3, story: 1, projects: [demo], supervisor: 'pm' })))
  const { w } = await open(hn, 3)
  hn.write.fn = () => ok({})
  await choose(w, 'story', '')
  await flushPromises()
  expect(hn.sent[0]?.body).toEqual({ story: null })
})

test('adding a dependency offers only what the rules accept and sends the chosen number', async () => {
  const { hn, w } = await mountTask(4)
  hn.write.fn = () => ok({})
  const add = w.get('[data-add-dependency]')
  await add.get('input').setValue('task')
  expect(add.findAll('[data-candidate]').map((b) => b.attributes('data-candidate'))).toEqual(['3'])
  await add.get('input').setValue('story')
  // Stories are offered, also a finished one; a cancelled ticket is not.
  expect(add.findAll('[data-candidate]').map((b) => b.attributes('data-candidate'))).toEqual(['1', '2', '5'])
  await add.get('[data-candidate="1"]').trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/tickets/4/dependencies', body: { depends_on: 1 } }])
})

test('the project field warns about a new project on another branch, not about projects the ticket has', async () => {
  const hn = await panelHarness((omo) => {
    omo.tickets = [ticket({ number: 1, projects: [demo] })]
    omo.projects = [
      { ...project('demo'), current_branch: 'topic', default_branch: 'main' },
      { ...project('other'), current_branch: 'feature-y', default_branch: 'main' },
    ]
  })
  const { w } = await open(hn, 1)
  await opener(w, 'projects').trigger('click')
  const notes = () => row(w, 'projects').findAll('[data-branch-notice]').map((n) => n.text())
  expect(notes()).toEqual([])
  await row(w, 'projects').get('input[value="other"]').setValue(true)
  expect(notes()).toHaveLength(1)
  expect(notes()[0]).toContain('other is on feature-y')
})

test('removing a dependency just added from this page removes it from the list at once', async () => {
  const { hn, w } = await mountTask(4)
  hn.write.fn = () => ok({}, hn.omo.eventId + 5)
  const add = w.get('[data-add-dependency]')
  await add.get('input').setValue('story')
  await add.get('[data-candidate="1"]').trigger('click')
  await flushPromises()
  expect(w.findAll('[data-dependency]').map((i) => i.get('a').text())).toEqual(['#1 Story one'])
  await w.get('[data-remove-dependency]').trigger('click')
  await flushPromises()
  expect(hn.sent.map((s) => s.method)).toEqual(['POST', 'DELETE'])
  expect(w.findAll('[data-dependency]')).toHaveLength(0)
})

test('a dependency the rules do not offer can still be tried; the server message is shown as it is', async () => {
  const hn = await panelHarness(seeded(...withStories, ticket({ number: 6, story: 1, title: 'In story', projects: [demo] })))
  const { w } = await open(hn, 4)
  hn.write.fn = () => refuse('dependency_scope', '#4 cannot depend on #6: a standalone task depends only on standalone tasks or stories')
  const add = w.get('[data-add-dependency]')
  await add.get('input').setValue('#6')
  expect(add.findAll('[data-candidate]')).toHaveLength(0)
  await add.get('[data-candidate-anyway]').trigger('click')
  await flushPromises()
  expect(row(w, 'depends_on').get('[data-field-error]').text()).toBe('#4 cannot depend on #6: a standalone task depends only on standalone tasks or stories')
  expect(row(w, 'depends_on').find('[data-dependency]').exists()).toBe(false)
})

test('dependencies are listed with number, title, column and why they wait; removing is one action', async () => {
  const t4 = ticket({ number: 4, projects: [demo], depends_on: [3, 1], waiting_for: [{ ticket: 3, reason: 'not_done' }, { ticket: 1, reason: 'export_not_merged' }] })
  const hn = await panelHarness(seeded(...withStories.filter((x) => x.number !== 4), t4))
  const { w } = await open(hn, 4)
  const items = w.findAll('[data-dependency]')
  expect(items.map((i) => i.get('a').text())).toEqual(['#3 The task', '#1 Story one'])
  expect(items[0]!.text()).toContain('Planning')
  expect(items[0]!.get('[data-unsatisfied]').text()).toBe('waiting for #3')
  expect(items[1]!.get('[data-unsatisfied]').text()).toBe('pull request not merged')
  expect(items[0]!.get('a').attributes('href')).toBe('/3')
  hn.write.fn = () => ok({}, hn.omo.eventId + 1)
  await items[0]!.get('[data-remove-dependency]').trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'DELETE', path: '/api/tickets/4/dependencies/3', body: undefined }])
  // Gone at once, until the stream says so.
  expect(w.findAll('[data-dependency]')).toHaveLength(1)
  expect(items[0]!.get('[data-remove-dependency]').attributes('aria-label')).toBe('Remove the dependency on #3 The task')
})

test('blocks lists what waits for this ticket as read-only links', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 4, projects: [demo], blocks: [3] }), withStories[2]!))
  const { w } = await open(hn, 4)
  const blocks = row(w, 'blocks')
  expect(blocks.get('a').text()).toBe('#3 The task')
  expect(blocks.find('button').exists()).toBe(false)
})

test('dates show local time with the exact time as a tooltip; the branch and pull request are facts', async () => {
  const t = ticket({
    number: 1,
    projects: [{ ...demo, branch: 'omo/1', export_url: 'https://example.com/pr/1' }],
    created_at: '2026-11-01T10:00:00Z',
    description_version: 3,
  })
  const hn = await panelHarness(seeded(t))
  const { w } = await open(hn, 1)
  const created = row(w, 'created').get('time')
  expect(created.attributes('datetime')).toBe('2026-11-01T10:00:00Z')
  expect(created.attributes('title')).toContain('2026-11-01T10:00:00Z')
  expect(row(w, 'description_version').text()).toContain('Version 3')
  expect(row(w, 'branch').text()).toContain('omo/1')
  expect(row(w, 'branch').get('a').attributes('href')).toBe('https://example.com/pr/1')
})

test('a working agent is named once at the top of the fields', async () => {
  const hn = await panelHarness(seeded(ticket({ number: 1, projects: [demo], column: 'in_progress', agents: [{ session: 's', role: 'developer', name: 'dev', state: 'running' }] })))
  const { w } = await open(hn, 1)
  expect(w.get('[data-agent-note]').text()).toContain('Changing a field adds a comment that tells it')
})
