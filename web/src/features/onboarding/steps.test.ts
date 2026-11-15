import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { Model, Profile, RoleSettings } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { harness, mountWith, type Harness } from '../create/dialogTesting'
import { useCreateTicket } from '../create/useCreateTicket'
import { resetSidebar, useSidebar } from '../sidebar/useSidebar'
import { useSettingsDialogs } from '../topbar/settingsMenu'
import { describeSetup, type SetupData } from './agentSetup'
import FirstSteps from './FirstSteps.vue'
import { firstSteps, registerStep } from './steps'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  useCreateTicket().close()
  useSettingsDialogs().close()
  resetSidebar()
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

const ctx = (projects: number, tickets: number, agentsReady: boolean | null, ceoEnabled = false) => ({ projects, tickets, agentsReady, ceoEnabled })

test('step 1 ticks when a project exists; step 2 when every needed role has a known model', () => {
  const [project1, agents, create] = firstSteps()
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
  // A model its profile switches off cannot be used.
  expect(describeSetup(data({ models: [model('claude/sonnet', false)] })).blocking).toHaveLength(3)
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
const stepText = (w: VueWrapper, id: string) => w.findAll('li.step')[['project', 'agents', 'ticket'].indexOf(id)]!.text()

test('the steps tick themselves from live data; the buttons open the settings pages and the dialogs', async () => {
  const h = await harness()
  h.respond.models = [model('claude/sonnet')]
  h.respond.profiles = { profiles: [profile('claude', ['sonnet'])], errors: [] }
  h.respond.roles = [role('pm', []), role('developer', []), role('reviewer', [])]
  const w = await mountSteps(h)
  await flushPromises()
  const items = w.findAll('li.step')
  expect(items).toHaveLength(3)
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
  const check = w.findAll('li.step')[1]!.get('button')
  expect(check.text()).toBe('Check the agent setup')
  await check.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/settings/roles?project=demo')
  const first = w.findAll('li.step')[0]!.findAll('button')
  expect(first.map((b) => b.text()).sort()).toEqual(['Add a workspace folder', 'Add project'])
  await first.find((b) => b.text() === 'Add a workspace folder')!.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/settings/projects?project=demo')
  await first.find((b) => b.text() === 'Add project')!.trigger('click')
  expect(useSettingsDialogs().state.open).toBe('addProject')
  await w.findAll('li.step')[2]!.get('button').trigger('click')
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

// Last: the registry is module state.
test('registered steps are placed after the one named', () => {
  registerStep({ id: 'extra', title: 'Extra', text: '', done: () => false, action: { label: 'Go', run: () => {} } }, 'project')
  expect(firstSteps().map((s) => s.id)).toEqual(['project', 'extra', 'agents', 'ticket'])
})
