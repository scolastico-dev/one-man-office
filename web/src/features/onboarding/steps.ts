import type { Router } from 'vue-router'
import type { ApiClient } from '../../api/client'
import { getSkillSets, putSkillSet } from '../../api/endpoints'
import { NO_REVISION } from '../../api/types'
import { settingsLocation } from '../../locations'
import { useCreateTicket } from '../create/useCreateTicket'
import { useSidebar } from '../sidebar/useSidebar'
import { hasSuperpowers, superpowersSkillSet } from '../skillsets/superpowers'
import { useSettingsDialogs } from '../topbar/settingsDialogs'

/** What the steps look at to tick themselves off. */
export interface StepContext {
  /** How many projects omo knows. */
  projects: number
  /** How many tickets exist, open or done. */
  tickets: number
  /** Whether every role that needs a model has a usable one; null while that is not known yet. */
  agentsReady: boolean | null
  /** Whether the CEO is switched on in the settings. */
  ceoEnabled: boolean
  /** Whether a skill set has the Superpowers repository; null while that is not known yet. */
  superpowers: boolean | null
}

/** What an action may use: the app's router (for the steps that open a settings page) and the API client. */
export interface StepNav {
  router: Router
  client?: ApiClient
}

export interface Step {
  id: string
  title: string
  text: string
  done(ctx: StepContext): boolean
  /** Recommended, never needed: it does not take the main button from the steps that are. */
  optional?: boolean
  /** Its button; a returned promise that rejects carries the server's refusal, shown in the step. */
  action: { label: string; run(nav: StepNav): void | Promise<void> }
  /** A second, quieter action; shown while `visible` says so (always when it is absent). */
  secondary?: { label: string; run(nav: StepNav): void; visible?(ctx: StepContext): boolean }
}

/** Opens a settings page, keeping the board's filters for the way back. */
const openSettings = (section: 'projects' | 'roles' | 'skills') => (nav: StepNav) => void nav.router.push(settingsLocation(section, nav.router.currentRoute.value))

/** Adds the Superpowers skill set in one click: created only (never over a set of the same name), under a free name. */
async function addSuperpowers(nav: StepNav): Promise<void> {
  if (!nav.client) return
  const { data } = await getSkillSets(nav.client)
  if (hasSuperpowers(data.skill_sets)) return
  await putSkillSet(nav.client, superpowersSkillSet(data.skill_sets.map((s) => s.name)), NO_REVISION)
}

function builtIn(): Step[] {
  return [
    {
      id: 'superpowers',
      title: 'Add the Superpowers skill set (recommended)',
      text: 'A public set of skills (brainstorming, test-driven development, code review) that omo fetches from GitHub and tells your agents to follow.',
      done: (c) => c.superpowers === true,
      optional: true,
      action: { label: 'Add Superpowers', run: addSuperpowers },
      secondary: { label: 'Skill sets page', run: openSettings('skills') },
    },
    {
      id: 'project',
      title: 'Add a project',
      text: 'A project is a folder with a git repository on this machine that omo may work in.',
      done: (c) => c.projects > 0,
      action: { label: 'Add project', run: () => useSettingsDialogs().open('addProject') },
      secondary: { label: 'Add a workspace folder', run: openSettings('projects') },
    },
    {
      id: 'agents',
      title: 'Check the agent setup',
      text: 'Which agent programs and models each role will use, and what is missing.',
      done: (c) => c.agentsReady === true,
      action: { label: 'Check the agent setup', run: openSettings('roles') },
    },
    {
      id: 'ticket',
      title: 'Create a first ticket',
      text: 'A story for a goal, or a task for one piece of work.',
      done: (c) => c.tickets > 0,
      action: { label: 'Create first ticket', run: () => useCreateTicket().open() },
      secondary: { label: 'Open the CEO tab', run: () => useSidebar().openOn('ceo'), visible: (c) => c.ceoEnabled },
    },
  ]
}

const added: { step: Step; after: string | undefined }[] = []

/** Adds a step after the step with id `after`, or at the end. */
export function registerStep(step: Step, after?: string): void {
  added.push({ step, after })
}

/** The steps in order: the built-in four (the first optional), with those registered placed among them. */
export function firstSteps(): Step[] {
  const list = builtIn()
  for (const { step, after } of added) {
    const i = after === undefined ? -1 : list.findIndex((s) => s.id === after)
    list.splice(i < 0 ? list.length : i + 1, 0, step)
  }
  return list
}
