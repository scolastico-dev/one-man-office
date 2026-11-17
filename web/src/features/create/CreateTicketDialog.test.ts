import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { ApiError } from '../../api/client'
import { afterEach, expect, test } from 'vitest'
import { activeToasts, useToast } from '../../components/ui/useToast'
import { general, project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import CreateTicketDialog from './CreateTicketDialog.vue'
import { harness, mountWith, ok, refuse, type Harness } from './dialogTesting'
import { STORY_TEMPLATE } from './createRules'
import { RELEASE_LABEL, REVIEW_LABEL, SETTING_LABEL, WHEN_LABEL } from '../ticket/fieldRules'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  for (const t of [...activeToasts]) useToast().dismiss(t.id)
})

const TWO = (omo: { projects: unknown[] }) => {
  omo.projects = [project('ui', 'ui'), project('api', 'api')]
}

async function open(h: Harness, props: Record<string, unknown> = {}) {
  const w = await mountWith(h, CreateTicketDialog, { open: true, ...props })
  mounted = w
  await flushPromises()
  return w
}

const input = (w: VueWrapper, name: string) => w.get(`[name="${name}"]`)
const errorUnder = (w: VueWrapper, field: string) => w.find(`[data-field="${field}"] .omo-field__error`).text()
const submit = async (w: VueWrapper) => {
  await w.get('form').trigger('submit')
  await flushPromises()
}

test('a task needs exactly one project; a story may have none', async () => {
  const h = await harness(TWO)
  h.respond.write = ({ body }) => ok({ ...ticket({ number: 7, title: 'T' }), description: '', body })
  const w = await open(h)

  // A story without any project is sent.
  await input(w, 'title').setValue('A story')
  await submit(w)
  expect(h.sent).toHaveLength(1)
  expect(h.sent[0]?.body).toEqual({ type: 'story', title: 'A story', projects: [] })

  // A task without a project is not sent; the field says why.
  await w.setProps({ open: false })
  await w.setProps({ open: true })
  await w.get('input[name="type"][value="task"]').setValue(true)
  await input(w, 'title').setValue('A task')
  await submit(w)
  expect(h.sent).toHaveLength(1)
  expect(errorUnder(w, 'projects')).toBe('Choose the project this task works in, or No project.')

  await w.get('input[type="radio"][value="api"]').setValue(true)
  await submit(w)
  expect(h.sent).toHaveLength(2)
  expect(h.sent[1]?.body).toEqual({ type: 'task', title: 'A task', projects: ['api'] })
})

test('No project is an explicit choice of a task: sent without projects and without "when approved"', async () => {
  const h = await harness(TWO)
  h.respond.write = () => ok({ ...ticket({ number: 7 }), description: '' })
  const w = await open(h)
  // A story has no such choice.
  expect(w.find('[data-no-project-option]').exists()).toBe(false)
  await w.get('input[name="type"][value="task"]').setValue(true)
  const none = w.get('[data-no-project-option] input')
  expect(none.attributes('value')).toBe('')
  expect((none.element as HTMLInputElement).checked).toBe(false)
  expect(w.find('[name="when_approved"]').exists()).toBe(true)

  await none.setValue(true)
  expect(w.find('[name="when_approved"]').exists()).toBe(false)
  expect(w.get('[data-field="projects"]').text()).toContain('Approving it merges and exports nothing')
  // A project chosen after it replaces the choice, and back.
  await w.get('input[type="radio"][value="api"]').setValue(true)
  expect((none.element as HTMLInputElement).checked).toBe(false)
  expect(w.find('[name="when_approved"]').exists()).toBe(true)
  await none.setValue(true)
  expect((w.get('input[type="radio"][value="api"]').element as HTMLInputElement).checked).toBe(false)

  await input(w, 'title').setValue('Research')
  await submit(w)
  expect(h.sent[0]?.body).toEqual({ type: 'task', title: 'Research', projects: [] })

  // Back to a story: the choice does not carry over.
  await w.setProps({ open: false })
  await w.setProps({ open: true })
  await w.get('input[name="type"][value="task"]').setValue(true)
  await w.get('[data-no-project-option] input').setValue(true)
  await w.get('input[name="type"][value="story"]').setValue(true)
  await input(w, 'title').setValue('A story')
  await submit(w)
  expect(h.sent[1]?.body).toEqual({ type: 'story', title: 'A story', projects: [] })
})

test('a single usable project is preselected', async () => {
  const h = await harness((omo) => (omo.projects = [project('ui', 'ui')]))
  h.respond.write = () => ok({ ...ticket({ number: 7 }), description: '' })
  const w = await open(h)
  await input(w, 'title').setValue('Story')
  await submit(w)
  expect(h.sent[0]?.body).toMatchObject({ projects: ['ui'] })
})

test('fields of More options are sent only when changed from their default', async () => {
  const h = await harness(TWO)
  h.respond.write = () => ok({ ...ticket({ number: 7 }), description: '' })
  const w = await open(h)
  await input(w, 'title').setValue('Plain')
  await submit(w)
  expect(h.sent[0]?.body).toEqual({ type: 'story', title: 'Plain', projects: [] })

  await w.setProps({ open: false })
  await w.setProps({ open: true })
  await input(w, 'title').setValue('Tuned')
  await input(w, 'review').setValue('human')
  await input(w, 'max_parallel').setValue('2')
  await input(w, 'depends_on').setValue('3, #4')
  await submit(w)
  expect(h.sent[1]?.body).toEqual({ type: 'story', title: 'Tuned', projects: [], review: 'human', max_parallel: 2, depends_on: [3, 4] })
})

test('review and when approved show the global default as the first option', async () => {
  const h = await harness(TWO)
  const w = await open(h)
  const first = (name: string) => w.get(`select[name="${name}"] option`).text()
  expect(first('review')).toBe('Default (Human)')
  expect(first('when_approved')).toBe('Default (Merge locally)')
  await w.get('input[name="type"][value="task"]').setValue(true)
  expect(first('review')).toBe('Default (Agent)')
})

test('a task in a story shows the default of tasks in a story, a standalone task that of tasks without one', async () => {
  const before = general.review_defaults.story_task
  general.review_defaults.story_task = 'skipped'
  try {
    const h = await harness((omo) => {
      TWO(omo)
      omo.tickets = [ticket({ number: 1, type: 'story', title: 'Story', projects: [{ project: 'ui', base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' }] })]
    })
    const w = await open(h, { initialStory: 1 })
    expect(w.get('select[name="review"] option').text()).toBe('Default (Skipped)')
    await w.get('select[name="story"]').setValue('')
    expect(w.get('select[name="review"] option').text()).toBe('Default (Agent)')
  } finally {
    general.review_defaults.story_task = before
  }
})

test('an empty supervisor reads as the server default; You sends user', async () => {
  const h = await harness(TWO)
  h.respond.write = () => ok({ ...ticket({ number: 7 }), description: '' })
  const w = await open(h)
  const labels = () => w.findAll('select[name="supervisor"] option').map((o) => o.text())
  expect(labels()).toEqual(['Default (You)', 'You'])
  await input(w, 'title').setValue('Plain')
  await submit(w)
  expect(h.sent[0]?.body).not.toHaveProperty('supervisor')
  // You is an explicit choice, so a task in a story does not get the product manager.
  await w.setProps({ open: false })
  await w.setProps({ open: true })
  await input(w, 'title').setValue('Mine')
  await input(w, 'supervisor').setValue('user')
  await submit(w)
  expect(h.sent[1]?.body).toMatchObject({ supervisor: 'user' })
})

test('the supervisor default of a task in a story is the product manager, and every model is listed', async () => {
  const h = await harness((omo) => {
    TWO(omo)
    omo.tickets = [ticket({ number: 1, type: 'story', title: 'Story', projects: [{ project: 'ui', base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' }] })]
  })
  h.respond.models = [
    { id: 'claude/sonnet', profile: 'claude', name: 'sonnet', cli: 'claude', description: '', selectable: true },
    { id: 'claude/hidden', profile: 'claude', name: 'hidden', cli: 'claude', description: '', selectable: false },
  ]
  const w = await open(h, { initialStory: 1 })
  expect(w.findAll('select[name="supervisor"] option').map((o) => o.text())).toEqual(['Default (Product manager)', 'You', 'Product manager'])
  expect(w.findAll('select[name="model"] option').map((o) => o.text())).toEqual([
    "Default (the role's model)",
    'claude/sonnet',
    'claude/hidden (not selectable by agents)',
  ])
})

test('tasks at once accepts any whole number from 1 up, as the server does', async () => {
  const h = await harness(TWO)
  h.respond.write = () => ok({ ...ticket({ number: 7 }), description: '' })
  const w = await open(h)
  await input(w, 'title').setValue('Big')
  await input(w, 'max_parallel').setValue('12345')
  await submit(w)
  expect(h.sent[0]?.body).toMatchObject({ max_parallel: 12345 })
  await w.setProps({ open: false })
  await w.setProps({ open: true })
  await input(w, 'title').setValue('Bad')
  await input(w, 'max_parallel').setValue('0')
  await submit(w)
  expect(h.sent).toHaveLength(1)
  expect(errorUnder(w, 'max_parallel')).toContain('whole number')
})

test('the story template is the start of a new story and left out of the request when untouched', async () => {
  const h = await harness(TWO)
  const w = await open(h)
  expect((input(w, 'description').element as HTMLTextAreaElement).value).toBe(STORY_TEMPLATE)
  await w.get('input[name="type"][value="task"]').setValue(true)
  expect((input(w, 'description').element as HTMLTextAreaElement).value).toBe('')
})

test('a validation error with details appears under its field and everything typed stays', async () => {
  const h = await harness(TWO)
  h.respond.write = () => refuse('validation', 'the title must have 1 to 200 characters', { field: 'title' })
  const w = await open(h)
  await input(w, 'title').setValue('Typed title')
  await input(w, 'description').setValue('Typed text')
  await submit(w)
  expect(errorUnder(w, 'title')).toBe('the title must have 1 to 200 characters')
  expect(w.find('[data-form-error]').exists()).toBe(false)
  expect((input(w, 'title').element as HTMLInputElement).value).toBe('Typed title')
  expect((input(w, 'description').element as HTMLTextAreaElement).value).toBe('Typed text')
  expect(w.get('[data-create-submit]').attributes('disabled')).toBeUndefined()
  expect((w.get('dialog').element as HTMLDialogElement).open).toBe(true)
})

test('the server error code and message name the field when there are no details', async () => {
  const h = await harness(TWO)
  h.respond.write = () => refuse('project_unavailable', 'project "ui" is missing on disk')
  const w = await open(h)
  await input(w, 'title').setValue('X')
  await submit(w)
  expect(errorUnder(w, 'projects')).toBe('project "ui" is missing on disk')

  h.respond.write = () => refuse('validation', 'model "x" does not exist')
  await submit(w)
  expect(errorUnder(w, 'model')).toBe('model "x" does not exist')

  h.respond.write = () => refuse('forbidden', 'not allowed', undefined, 403)
  await submit(w)
  expect(w.get('[data-form-error]').text()).toBe('not allowed')
})

test('obvious problems are refused in the dialog without a request', async () => {
  const h = await harness(TWO)
  const w = await open(h)
  await submit(w)
  expect(errorUnder(w, 'title')).toBe('Enter a title.')
  await input(w, 'title').setValue('x'.repeat(201))
  await submit(w)
  expect(errorUnder(w, 'title')).toContain('at most 200')
  await input(w, 'title').setValue('bell\u0007')
  await submit(w)
  expect(errorUnder(w, 'title')).toContain('control characters')
  for (const hidden of ['evil\u202Etxt', 'zero\u200Bwidth', `tag${String.fromCodePoint(0xe0041)}`]) {
    await input(w, 'title').setValue(hidden)
    await submit(w)
    expect(errorUnder(w, 'title')).toContain('control characters')
  }
  expect(h.sent).toHaveLength(0)
})

test('while the request runs the button is disabled and nothing is sent twice; success closes and announces', async () => {
  const h = await harness(TWO)
  let finish!: () => void
  h.respond.write = () => new Promise((resolve) => (finish = () => resolve(ok({ ...ticket({ number: 12, title: 'Done thing' }), description: '' }))))
  const w = await open(h)
  await input(w, 'title').setValue('Done thing')
  await w.get('form').trigger('submit')
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(h.sent).toHaveLength(1)
  expect(w.get('[data-create-submit]').attributes('disabled')).toBeDefined()
  finish()
  await flushPromises()
  const dialog = w.findComponent(CreateTicketDialog)
  expect(dialog.emitted('created')).toEqual([[12]])
  expect(dialog.emitted('close')).toHaveLength(1)
  expect(activeToasts.map((t) => t.title)).toEqual(['#12 created'])
})

test('Create another keeps the dialog open with an empty title', async () => {
  const h = await harness(TWO)
  h.respond.write = () => ok({ ...ticket({ number: 5 }), description: '' })
  const w = await open(h)
  await w.get('.create__another input').setValue(true)
  await input(w, 'title').setValue('First')
  await submit(w)
  const dialog = w.findComponent(CreateTicketDialog)
  expect(dialog.emitted('close')).toBeUndefined()
  expect((input(w, 'title').element as HTMLInputElement).value).toBe('')
})

test('Escape closes at once when nothing was typed and asks before discarding typed text', async () => {
  const h = await harness(TWO)
  const w = await open(h)
  const dialog = w.findComponent(CreateTicketDialog)
  await w.get('dialog').trigger('keydown', { key: 'Escape' })
  expect(dialog.emitted('close')).toHaveLength(1)

  await input(w, 'title').setValue('Half a thought')
  await w.get('dialog').trigger('keydown', { key: 'Escape' })
  await flushPromises()
  expect(dialog.emitted('close')).toHaveLength(1)
  expect(w.text()).toContain('Discard what you typed?')
  // Escape again keeps editing.
  await w.get('dialog').trigger('keydown', { key: 'Escape' })
  expect(w.text()).not.toContain('Discard what you typed?')
  expect((input(w, 'title').element as HTMLInputElement).value).toBe('Half a thought')

  await w.get('dialog').trigger('keydown', { key: 'Escape' })
  const discard = w.findAll('button').find((b) => b.text() === 'Discard')
  await discard!.trigger('click')
  expect(dialog.emitted('close')).toHaveLength(2)
})

test('a task with a story offers only that story\'s projects and hides when approved', async () => {
  const h = await harness((omo) => {
    TWO(omo)
    omo.tickets = [
      ticket({ number: 1, type: 'story', title: 'S', projects: [{ project: 'api', base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' }] }),
      ticket({ number: 2, type: 'story', title: 'Closed', column: 'review' }),
    ]
  })
  const w = await open(h, { initialStory: 1 })
  expect((input(w, 'story').element as HTMLSelectElement).value).toBe('1')
  expect([...(input(w, 'story').element as HTMLSelectElement).options].map((o) => o.value)).toEqual(['', '1'])
  // The story's project, or none.
  expect(w.findAll('input[type="radio"][name$="-project"]').map((r) => r.attributes('value'))).toEqual(['', 'api'])
  expect(w.find('[name="when_approved"]').exists()).toBe(false)
  await input(w, 'title').setValue('In story')
  h.respond.write = () => ok({ ...ticket({ number: 9 }), description: '' })
  await submit(w)
  expect(h.sent[0]?.body).toEqual({ type: 'task', title: 'In story', projects: ['api'], story: 1 })
})

test('Add project… adds the project and selects it', async () => {
  const h = await harness((omo) => (omo.projects = []))
  const w = await open(h)
  expect(w.text()).toContain('No project yet.')
  h.respond.write = ({ path, body }) => {
    if (path === '/api/projects') return ok({ ...project('new', 'new'), path: (body as { path: string }).path })
    return ok({ ...ticket({ number: 3 }), description: '' })
  }
  await w.get('[data-add-project]').trigger('click')
  await flushPromises()
  const folder = w.get('dialog dialog input[name="path"]')
  await folder.setValue('/work/new')
  await w.get('dialog dialog form').trigger('submit')
  await flushPromises()
  expect(h.sent[0]).toMatchObject({ method: 'POST', path: '/api/projects', body: { path: '/work/new' } })
  const radio = w.get('input[type="checkbox"][value="new"]')
  expect((radio.element as HTMLInputElement).checked).toBe(true)
  await input(w, 'title').setValue('With the new project')
  await submit(w)
  expect(h.sent[1]?.body).toMatchObject({ projects: ['new'] })
})

test('adding a project after a title was typed does not submit the create form (dialogs sit in the body)', async () => {
  const h = await harness((omo) => (omo.projects = []))
  const w = await mountWith(h, CreateTicketDialog, { open: true }, { teleport: true })
  mounted = w
  await flushPromises()
  h.respond.write = ({ path, body }) => {
    if (path === '/api/projects') return ok({ ...project('new', 'new'), path: (body as { path: string }).path })
    return ok({ ...ticket({ number: 3 }), description: '' })
  }
  const title = document.querySelector<HTMLInputElement>('input[name="title"]')!
  title.value = 'Typed first'
  title.dispatchEvent(new Event('input'))
  document.querySelector<HTMLElement>('[data-add-project]')!.click()
  await flushPromises()
  const add = document.querySelector<HTMLDialogElement>('dialog[open] input[name="path"]')!.closest('dialog')!
  expect(add.closest('form')).toBeNull()
  const folder = add.querySelector<HTMLInputElement>('input[name="path"]')!
  folder.value = '/work/new'
  folder.dispatchEvent(new Event('input'))
  add.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  await flushPromises()
  expect(h.sent.map((s) => s.path)).toEqual(['/api/projects'])
  expect(document.querySelector<HTMLInputElement>('input[type="checkbox"][value="new"]')!.checked).toBe(true)
  expect(title.value).toBe('Typed first')
})

test('a task in a story gets no branch notice: it is based on its story\'s branch', async () => {
  const onFeature = { ...project('ui', 'ui'), current_branch: 'feature-x', default_branch: 'main' }
  const h = await harness((omo) => {
    omo.projects = [onFeature]
    omo.tickets = [ticket({ number: 1, type: 'story', title: 'Story', projects: [{ project: 'ui', base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' }] })]
  })
  const w = await open(h, { initialStory: 1 })
  expect((w.get('input[type="radio"][value="ui"]').element as HTMLInputElement).checked).toBe(true)
  expect(w.find('[data-branch-notice]').exists()).toBe(false)
  // A standalone task with the same project does get it.
  await w.get('select[name="story"]').setValue('')
  await w.get('input[type="radio"][value="ui"]').setValue(true)
  expect(w.findAll('[data-branch-notice]')).toHaveLength(1)
})

test('a lost answer on create says the ticket may exist and keeps the form', async () => {
  const h = await harness(TWO)
  h.respond.write = () => {
    throw new ApiError(0, 'network', 'Cannot reach omo. Check that it is still running.')
  }
  const w = await open(h)
  await input(w, 'title').setValue('Maybe made')
  await submit(w)
  expect(w.get('[data-form-error]').text()).toContain('the ticket may have been created')
  expect(w.get('[data-form-error]').text()).toContain('before you press Create again')
  expect((input(w, 'title').element as HTMLInputElement).value).toBe('Maybe made')
  expect(w.get('[data-create-submit]').attributes('disabled')).toBeUndefined()
})

test('a lost answer on Add project says the project may have been added', async () => {
  const h = await harness((omo) => (omo.projects = []))
  const w = await open(h)
  h.respond.write = () => {
    throw new ApiError(0, 'network', 'Cannot reach omo. Check that it is still running.')
  }
  await w.get('[data-add-project]').trigger('click')
  await flushPromises()
  await w.get('dialog dialog input[name="path"]').setValue('/work/new')
  await w.get('dialog dialog form').trigger('submit')
  await flushPromises()
  expect(w.get('dialog dialog .omo-field__error').text()).toContain('the project may have been added')
})

test('a chosen project on a branch other than its default carries a notice; others none', async () => {
  const h = await harness((omo) => {
    omo.projects = [
      { ...project('ui', 'ui'), current_branch: 'feature-x', default_branch: 'main' },
      { ...project('api', 'api'), current_branch: 'main', default_branch: 'main' },
      { ...project('web', 'web'), current_branch: 'topic' },
      { ...project('det', 'det'), default_branch: 'main' },
    ]
  })
  const w = await open(h)
  await w.get('input[name="type"][value="task"]').setValue(true)
  await w.get('input[type="radio"][value="api"]').setValue(true)
  expect(w.find('[data-branch-notice]').exists()).toBe(false)
  await w.get('input[type="radio"][value="web"]').setValue(true)
  expect(w.find('[data-branch-notice]').exists()).toBe(false)
  await w.get('input[type="radio"][value="det"]').setValue(true)
  expect(w.find('[data-branch-notice]').exists()).toBe(false)
  await w.get('input[type="radio"][value="ui"]').setValue(true)
  expect(w.findAll('[data-branch-notice]')).toHaveLength(1)
  expect(w.get('[data-branch-notice]').text().replace(/\s+/g, ' ')).toBe(
    'ui is on feature-x, not its default branch main. The ticket will be based on feature-x.',
  )
})

test('with more than eight projects a filter narrows the list; the chosen project stays in it', async () => {
  const names = ['alpha', 'bravo', 'charlie', 'delta', 'echo', 'foxtrot', 'golf', 'hotel', 'india', 'juliet']
  const h = await harness((omo) => {
    omo.projects = names.map((n) => project(n, n))
  })
  const w = await open(h)
  const visible = () => w.findAll('fieldset input[name$="-project"]').map((i) => (i.element as HTMLInputElement).value)
  expect(visible()).toHaveLength(10)
  expect(w.get('[data-project-count]').text()).toBe('10 projects')
  await w.get('input[value="bravo"]').setValue(true)
  const filter = w.get('[data-project-filter]')
  expect(w.get(`label[for="${filter.attributes('id')}"]`).text()).toBe('Filter projects')
  await filter.setValue('ec')
  expect(visible()).toEqual(['bravo', 'echo'])
  expect(w.get('[data-project-count]').text()).toBe('1 of 10 projects match')
  expect((w.get('input[value="bravo"]').element as HTMLInputElement).checked).toBe(true)
  await filter.setValue('zzz')
  expect(visible()).toEqual(['bravo'])
  // Enter in the filter does not send the form.
  await filter.trigger('keydown', { key: 'Enter' })
  expect(h.sent.filter((s) => s.method === 'POST')).toEqual([])
})

test('with eight projects or fewer there is no filter', async () => {
  const h = await harness(TWO)
  const w = await open(h)
  expect(w.find('[data-project-filter]').exists()).toBe(false)
})

test('the optional settings are worded as in the ticket details and the settings pages', async () => {
  const h = await harness(TWO)
  const w = await open(h)
  const options = (name: string) => w.findAll(`select[name="${name}"] option`).map((o) => o.text())
  expect(options('review').slice(1)).toEqual(Object.values(REVIEW_LABEL))
  expect(options('when_approved').slice(1)).toEqual(Object.values(WHEN_LABEL))
  expect(options('task_release').slice(1)).toEqual(Object.values(RELEASE_LABEL))
  const label = (field: string) => w.get(`[data-field="${field}"] label`).text()
  expect(label('task_release')).toBe(SETTING_LABEL.taskRelease)
  expect(label('max_parallel')).toBe(SETTING_LABEL.maxParallel)
})
