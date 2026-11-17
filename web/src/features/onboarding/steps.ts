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
  /** Whether any agent profile exists (false: omo found no agent program); null while that is not known yet. */
  hasProfiles?: boolean | null
  /** Whether the CEO is switched on in the settings. */
  ceoEnabled: boolean
  /** Whether tickets the CEO creates wait for the user's approval. */
  ceoTicketsWait?: boolean
  /** Whether a skill set has the Superpowers repository; null while that is not known yet. */
  superpowers: boolean | null
  /** Whether a profile runs Antigravity (agy) while omo's MCP endpoint is on: only then is omo's entry in agy's configuration needed. */
  agyNeedsEntry?: boolean
  /** Whether omo's entry is in Antigravity's configuration; null while that is not known yet. */
  agyRegistered?: boolean | null
}

/** What an action may use: the app's router (for the steps that open a settings page) and the API client. */
export interface StepNav {
  router: Router
  client?: ApiClient
}

/** A value of a step that may depend on what the steps look at. */
export type ByContext<T> = T | ((ctx: StepContext) => T)

/** The value for ctx. */
export function forContext<T>(v: ByContext<T>, ctx: StepContext): T {
  return typeof v === 'function' ? (v as (ctx: StepContext) => T)(ctx) : v
}

export interface Step {
  id: string
  title: string
  text: ByContext<string>
  done(ctx: StepContext): boolean
  /** Whether the step applies at all (always when absent). */
  shown?(ctx: StepContext): boolean
  /** Recommended, never needed: it does not take the main button from the steps that are. */
  optional?: boolean
  /** Its button; a returned promise that rejects carries the server's refusal, shown in the step. */
  action: { label: ByContext<string>; run(nav: StepNav, ctx: StepContext): void | Promise<void> }
  /** A second, quieter action; shown while `visible` says so (always when it is absent). */
  secondary?: { label: string; run(nav: StepNav): void; visible?(ctx: StepContext): boolean }
}

/** Opens a settings page, keeping the board's filters for the way back. */
const openSettings = (section: 'projects' | 'agents' | 'roles' | 'skills' | 'mcp') => (nav: StepNav) => void nav.router.push(settingsLocation(section, nav.router.currentRoute.value))

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
      text: (c) =>
        c.hasProfiles === false
          ? 'omo found no agent program (Claude Code, Codex, Antigravity or Gemini CLI) on its PATH. Install one and sign in to it once, then let omo detect it on the Agents page.'
          : 'Which agent programs and models each role will use, and what is missing.',
      done: (c) => c.agentsReady === true,
      action: {
        label: (c) => (c.hasProfiles === false ? 'Detect agent programs' : 'Check the agent setup'),
        run: (nav, c) => openSettings(c.hasProfiles === false ? 'agents' : 'roles')(nav),
      },
    },
    {
      id: 'agy-mcp',
      title: 'Register omo in Antigravity (recommended)',
      text: "Claude Code and Codex agents get omo's MCP entry with every session; Antigravity reads it only from its own configuration. Without it, Antigravity agents use the omo ai command line.",
      shown: (c) => c.agyNeedsEntry === true,
      done: (c) => c.agyRegistered === true,
      optional: true,
      action: { label: 'MCP settings', run: openSettings('mcp') },
    },
    {
      id: 'ticket',
      title: 'Create a first ticket',
      text: (c) =>
        'A story for a goal, or a task for one piece of work. It waits in Planning until you approve it (Approve in the ticket, or drag it to Ready For Dev); then an agent starts.' +
        (c.ceoEnabled && c.ceoTicketsWait ? ' So do tickets the CEO drafts.' : ''),
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

/** The steps in order: the built-in ones (Superpowers and the Antigravity entry optional), with those registered placed among them. */
export function firstSteps(): Step[] {
  const list = builtIn()
  for (const { step, after } of added) {
    const i = after === undefined ? -1 : list.findIndex((s) => s.id === after)
    list.splice(i < 0 ? list.length : i + 1, 0, step)
  }
  return list
}
