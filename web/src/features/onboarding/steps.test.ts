import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { Model, Profile, RoleSettings } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { harness, mountWith, ok, refuse, type Harness } from '../create/dialogTesting'
import { useCreateTicket } from '../create/useCreateTicket'
import { resetSidebar, useSidebar } from '../sidebar/useSidebar'
import { useSettingsDialogs } from '../topbar/settingsDialogs'
import { describeSetup, type SetupData } from './agentSetup'
import { FIRST_STEPS_KEY, resetDismissed } from './dismissed'
import FirstSteps from './FirstSteps.vue'
import { firstSteps, forContext, type StepContext } from './steps'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  useCreateTicket().close()
  useSettingsDialogs().close()
  resetSidebar()
  localStorage.clear()
  resetDismissed()
})

const model = (id: string, selectable = true): Model => {
  const [profile = '', name = ''] = id.split('/')
  return { id, profile, name, cli: 'claude', description: '', selectable }
}
const role = (name: string, models: string[]): RoleSettings => ({ name, models, assignment: 'round_robin' })
const profile = (name: string, models: string[]): Profile => ({
  name,
  cli: 'claude',
  command: 'claude',
  args: null,
  env: null,
  account_dir: '',
  usage_script: '',
  prompt_delivery: {},
  models: Object.fromEntries(models.map((m) => [m, { args: null, env: null, usage_windows: null, description: '' }])),
})

function data(over: Partial<SetupData> = {}): SetupData {
  return {
    roles: [role('ceo', []), role('pm', ['claude/sonnet']), role('developer', ['claude/sonnet']), role('reviewer', ['claude/sonnet'])],
    profiles: [profile('claude', ['sonnet'])],
    models: [model('claude/sonnet')],
    profileErrors: [],
    roleErrors: [],
    ceoEnabled: false,
    ...over,
  }
}

const ctx = (projects: number, tickets: number, agentsReady: boolean | null, ceoEnabled = false, superpowers: boolean | null = null) => ({ projects, tickets, agentsReady, ceoEnabled, superpowers })

test('the first step offers Superpowers, recommended and optional; it is done once a set has its repository', () => {
  const [superpowers, project1] = firstSteps()
  expect(superpowers!.id).toBe('superpowers')
  expect(superpowers!.title).toBe('Add the Superpowers skill set (recommended)')
  expect(superpowers!.optional).toBe(true)
  expect(superpowers!.done(ctx(0, 0, null, false, null))).toBe(false)
  expect(superpowers!.done(ctx(0, 0, null, false, false))).toBe(false)
  expect(superpowers!.done(ctx(0, 0, null, false, true))).toBe(true)
  expect(superpowers!.action.label).toBe('Add Superpowers')
  expect(superpowers!.secondary!.label).toBe('Skill sets page')
  expect(project1!.id).toBe('project')
})

test('step 1 ticks when a project exists; step 2 when every needed role has a known model', () => {
  const byId = (id: string) => firstSteps().find((s) => s.id === id)
  const [project1, agents, create] = [byId('project'), byId('agents'), byId('ticket')]
  expect(project1!.done(ctx(0, 0, null))).toBe(false)
  expect(project1!.done(ctx(1, 0, null))).toBe(true)
  expect(agents!.done(ctx(0, 0, null))).toBe(false)
  expect(agents!.done(ctx(0, 0, false))).toBe(false)
  expect(agents!.done(ctx(0, 0, true))).toBe(true)
  expect(create!.done(ctx(0, 1, null))).toBe(true)

  expect(describeSetup(data()).blocking).toEqual([])
  const missing = describeSetup(data({ roles: [role('pm', ['claude/sonnet']), role('developer', []), role('reviewer', ['gone/model'])] }))
  expect(missing.blocking.map((r) => r.name)).toEqual(['developer', 'reviewer'])
  // A role file that does not exist counts as a role without a model.
  expect(describeSetup(data({ roles: [] })).blocking.map((r) => r.name)).toEqual(['pm', 'developer', 'reviewer'])
  // A model that agents may not choose for tickets can still be a role's model.
  expect(describeSetup(data({ models: [model('claude/sonnet', false)] })).blocking).toEqual([])
})

test('step 2 ignores the CEO role while the CEO is off', () => {
  expect(describeSetup(data({ ceoEnabled: false })).blocking).toEqual([])
  const on = describeSetup(data({ ceoEnabled: true }))
  expect(on.blocking.map((r) => r.name)).toEqual(['ceo'])
  expect(on.roles[0]!.problems).toEqual(['No model assigned. Nothing starts for the CEO until it has one.'])
  const off = describeSetup(data({ ceoEnabled: false })).roles[0]!
  expect(off.off).toBe(true)
  expect(off.problems).toEqual([])
})

test('a role naming a model that no longer exists and a broken file are shown as what they are', () => {
  const r = describeSetup(
    data({
      roles: [role('pm', ['claude/sonnet', 'claude/gone']), role('developer', ['claude/gone']), role('reviewer', ['claude/sonnet'])],
      roleErrors: [{ file: '/home/omo/roles/reviewer.yaml', message: 'line 2: bad' }],
      profileErrors: [{ file: '/home/omo/profiles/x.yaml', message: 'cli must be claude' }],
    }),
  )
  const pm = r.roles.find((x) => x.name === 'pm')!
  expect(pm.usable).toBe(1)
  expect(pm.problems).toEqual(['claude/gone does not exist (any more): no profile defines it.'])
  expect(r.roles.find((x) => x.name === 'developer')!.problems.at(-1)).toContain('Nothing starts for the developer')
  expect(r.roles.find((x) => x.name === 'reviewer')!.problems[0]).toContain('/home/omo/roles/reviewer.yaml cannot be used: line 2: bad')
  expect(r.profileErrors).toHaveLength(1)
})

async function mountSteps(h: Harness) {
  const w = await mountWith(h, FirstSteps, {})
  mounted = w
  return w
}
const IDS = ['superpowers', 'project', 'agents', 'ticket']
const stepText = (w: VueWrapper, id: string) => w.findAll('li.step')[IDS.indexOf(id)]!.text()
const stepOf = (w: VueWrapper, id: string) => w.findAll('li.step')[IDS.indexOf(id)]!

test('the steps tick themselves from live data; the buttons open the settings pages and the dialogs', async () => {
  const h = await harness()
  h.respond.models = [model('claude/sonnet')]
  h.respond.profiles = { profiles: [profile('claude', ['sonnet'])], errors: [] }
  h.respond.roles = [role('pm', []), role('developer', []), role('reviewer', [])]
  const w = await mountSteps(h)
  await flushPromises()
  const items = w.findAll('li.step')
  expect(items).toHaveLength(4)
  expect(w.text()).toContain('This list disappears once the board has a ticket.')
  expect(w.text()).toContain('a git repository on this machine')
  for (const id of ['project', 'agents', 'ticket']) expect(stepText(w, id)).not.toContain('done')

  // A project added elsewhere (or by hand) ticks the first step.
  h.omo.emit('project.changed', { project: project('ui') })
  await flushPromises()
  expect(stepText(w, 'project')).toContain('done')
  expect(stepText(w, 'agents')).not.toContain('done')

  // Roles get models in the settings files.
  h.respond.roles = [role('pm', ['claude/sonnet']), role('developer', ['claude/sonnet']), role('reviewer', ['claude/sonnet'])]
  h.omo.emit('settings.changed', { section: 'roles', name: 'developer' })
  await flushPromises()
  expect(stepText(w, 'agents')).toContain('done')
  expect(stepText(w, 'ticket')).not.toContain('done')

  // A role loses its model again.
  h.respond.roles = [role('pm', ['claude/sonnet']), role('developer', []), role('reviewer', ['claude/sonnet'])]
  h.omo.emit('settings.changed', { section: 'roles', name: 'developer' })
  await flushPromises()
  expect(stepText(w, 'agents')).not.toContain('done')

  // The agent setup and the workspace folders are settings pages; adding one project stays a dialog.
  const router = w.vm.$router
  await router.push('/?project=demo')
  const check = stepOf(w, 'agents').get('button')
  expect(check.text()).toBe('Check the agent setup')
  await check.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/settings/roles?project=demo')
  const first = stepOf(w, 'project').findAll('button')
  expect(first.map((b) => b.text()).sort()).toEqual(['Add a workspace folder', 'Add project'])
  await first.find((b) => b.text() === 'Add a workspace folder')!.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/settings/projects?project=demo')
  await first.find((b) => b.text() === 'Add project')!.trigger('click')
  expect(useSettingsDialogs().state.open).toBe('addProject')
  await stepOf(w, 'ticket').get('button').trigger('click')
  expect(useCreateTicket().state.open).toBe(true)

  h.omo.change('ticket.created', ticket({ number: 1, title: 'First' }))
  await flushPromises()
  expect(stepText(w, 'ticket')).toContain('done')
})

test('first steps offer "Open the CEO tab" only while the CEO is enabled', () => {
  const create = firstSteps().find((s) => s.id === 'ticket')!
  expect(create.secondary!.label).toBe('Open the CEO tab')
  expect(create.secondary!.visible!(ctx(0, 0, null, false))).toBe(false)
  expect(create.secondary!.visible!(ctx(0, 0, null, true))).toBe(true)
  create.secondary!.run({ router: createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] }) })
  expect(useSidebar().tab.value).toBe('ceo')
  expect(useSidebar().how.value).toBe('user')
})

test('Add Superpowers creates the set in one click (create only), the step ticks from the server; the main button stays on the first required step', async () => {
  const h = await harness()
  const w = await mountSteps(h)
  await flushPromises()
  const step = stepOf(w, 'superpowers')
  expect(step.text()).toContain('Add the Superpowers skill set (recommended)')
  expect(step.text()).not.toContain('done')
  // Optional: it never takes the main button from the steps that are needed.
  expect(step.get('[data-step-action]').classes()).not.toContain('omo-btn--primary')
  expect(stepOf(w, 'project').get('[data-step-action]').classes()).toContain('omo-btn--primary')

  h.respond.skillSets = { skill_sets: [{ name: 'superpowers', repository: 'https://example.com/other.git', ref: '', update: 'pinned', enabled: true, paths: [], roles: [], instructions: {} }], states: [], errors: [] }
  h.respond.write = (s) => {
    h.respond.skillSets = { skill_sets: [...h.respond.skillSets.skill_sets, s.body as never], states: [], errors: [] }
    return ok(s.body)
  }
  await step.get('[data-step-action]').trigger('click')
  await flushPromises()
  // The name is taken by another set: the next free one; created only, never over a set.
  expect(h.sent).toHaveLength(1)
  expect(h.sent[0]).toMatchObject({ method: 'PUT', path: '/api/skillsets/superpowers-2', ifMatch: '0' })
  expect(h.sent[0]!.body).toMatchObject({ name: 'superpowers-2', repository: 'https://github.com/obra/superpowers.git', ref: 'main', update: 'at_start', enabled: true })
  h.omo.emit('skillset.changed', { name: 'superpowers-2', state: 'updating', commit: '', path: '', updated_at: null, message: '' })
  await flushPromises()
  expect(stepText(w, 'superpowers')).toContain('done')

  // The link opens the Skill sets page.
  const router = w.vm.$router
  await stepOf(w, 'superpowers').get('[data-step-secondary]').trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/settings/skills')
})

test("the server's refusal of Add Superpowers shows in its step", async () => {
  const h = await harness()
  h.respond.write = () => refuse('validation', 'repository: the address cannot be reached', { field: 'repository' })
  const w = await mountSteps(h)
  await flushPromises()
  await stepOf(w, 'superpowers').get('[data-step-action]').trigger('click')
  await flushPromises()
  expect(stepOf(w, 'superpowers').get('[data-step-error]').text()).toBe('repository: the address cannot be reached')
  expect(stepText(w, 'superpowers')).not.toContain('done')
})

const memoryRouter = () => createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })

test('without any agent profile the agent step says no program was found and opens the Agents page to detect one', async () => {
  const agents = firstSteps().find((s) => s.id === 'agents')!
  const none: StepContext = { ...ctx(0, 0, false), hasProfiles: false }
  expect(forContext(agents.text, none)).toContain('omo found no agent program (Claude Code, Codex, Antigravity or Gemini CLI) on its PATH')
  expect(forContext(agents.action.label, none)).toBe('Detect agent programs')
  const router = memoryRouter()
  agents.action.run({ router }, none)
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/settings/agents')
  const some: StepContext = { ...ctx(0, 0, false), hasProfiles: true }
  expect(forContext(agents.action.label, some)).toBe('Check the agent setup')
  agents.action.run({ router }, some)
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/settings/roles')
})

test('the Antigravity step applies only to an Antigravity profile while MCP is on, is recommended, and ticks once omo is registered there', async () => {
  const agy = firstSteps().find((s) => s.id === 'agy-mcp')!
  expect(agy.optional).toBe(true)
  expect(agy.shown!(ctx(0, 0, null))).toBe(false)
  expect(agy.shown!({ ...ctx(0, 0, null), agyNeedsEntry: true })).toBe(true)
  expect(agy.done({ ...ctx(0, 0, null), agyNeedsEntry: true, agyRegistered: false })).toBe(false)
  expect(agy.done({ ...ctx(0, 0, null), agyNeedsEntry: true, agyRegistered: true })).toBe(true)
  const router = memoryRouter()
  agy.action.run({ router }, ctx(0, 0, null))
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/settings/mcp')
})

test("the ticket step says a new ticket waits in Planning for approval, and so do the CEO's while that switch is on", () => {
  const create = firstSteps().find((s) => s.id === 'ticket')!
  const plain = forContext(create.text, ctx(0, 0, null))
  expect(plain).toContain('It waits in Planning until you approve it (Approve in the ticket, or drag it to Ready For Dev); then an agent starts.')
  expect(plain).not.toContain('CEO')
  expect(forContext(create.text, { ...ctx(0, 0, null, true), ceoTicketsWait: true })).toContain('So do tickets the CEO drafts.')
  expect(forContext(create.text, { ...ctx(0, 0, null, true), ceoTicketsWait: false })).not.toContain('CEO')
})

test('an Antigravity profile shows the step, read from the agent programs; dismissing a step or hiding the list is remembered', async () => {
  const h = await harness()
  h.respond.profiles = { profiles: [{ ...profile('agy', ['flash']), cli: 'agy', command: 'agy' }], errors: [] }
  h.omo.mcp.clients = [{ target: { id: 'agy', cli: 'agy', account: '', config_path: '/home/u/.gemini/antigravity/mcp_config.json' }, state: 'absent', entry: null, entries: [], error: '' }]
  const w = await mountSteps(h)
  await flushPromises()
  const titles = () => w.findAll('li.step h3').map((x) => x.text())
  expect(titles()).toContain('Register omo in Antigravity (recommended)')
  const agyStep = () => w.findAll('li.step').find((x) => x.text().includes('Register omo in Antigravity'))
  expect(agyStep()!.text()).not.toContain('done')

  await agyStep()!.get('[data-step-dismiss]').trigger('click')
  expect(titles()).not.toContain('Register omo in Antigravity (recommended)')
  expect(JSON.parse(localStorage.getItem(FIRST_STEPS_KEY)!)).toEqual({ steps: ['agy-mcp'], hidden: false })
  // Done steps cannot be dismissed (nothing left to do); open ones can.
  expect(w.findAll('[data-step-dismiss]').length).toBe(w.findAll('li.step').filter((x) => !x.text().includes('done')).length)

  await w.get('[data-steps-hide]').trigger('click')
  expect(w.find('[aria-label="First steps"]').exists()).toBe(false)
  expect(JSON.parse(localStorage.getItem(FIRST_STEPS_KEY)!)).toEqual({ steps: ['agy-mcp'], hidden: true })
  // Another mount in this browser reads it back.
  w.unmount()
  mounted = null
  resetDismissed()
  const again = await mountSteps(h)
  await flushPromises()
  expect(again.find('[aria-label="First steps"]').exists()).toBe(false)
})

test('omo registered in Antigravity ticks the step', async () => {
  const h = await harness()
  h.respond.profiles = { profiles: [{ ...profile('agy', ['flash']), cli: 'agy', command: 'agy' }], errors: [] }
  h.omo.mcp.clients = [{ target: { id: 'agy', cli: 'agy', account: '', config_path: '/home/u/.gemini/antigravity/mcp_config.json' }, state: 'installed', entry: {}, entries: [], error: '' }]
  const w = await mountSteps(h)
  await flushPromises()
  expect(w.findAll('li.step').find((x) => x.text().includes('Register omo in Antigravity'))!.text()).toContain('done')
})

test('without an Antigravity profile the agent programs are not read', async () => {
  const h = await harness()
  const reads: string[] = []
  const get = h.session.client.get.bind(h.session.client)
  h.session.client.get = (<T,>(path: string, q?: never) => {
    reads.push(path)
    return get<T>(path, q)
  }) as typeof h.session.client.get
  const w = await mountSteps(h)
  await flushPromises()
  expect(w.findAll('li.step h3').map((x) => x.text())).not.toContain('Register omo in Antigravity (recommended)')
  expect(reads).not.toContain('/api/mcp/clients')
})
