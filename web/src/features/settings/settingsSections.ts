// The sections of the settings: their names in the section list and the page
// each one shows. Pages are loaded only when shown, so the board's first load
// carries none of their code.
import type { Component, InjectionKey, Ref } from 'vue'
import { isSettingsSection, type SettingsSection } from '../../locations'

export interface SettingsSectionDef {
  id: SettingsSection
  /** The name in the section list and the page's title. */
  label: string
  /** One line under the name and the title, without a final full stop. */
  description: string
  /** Loads the page. */
  component: () => Promise<Component>
}

const page = (m: { default: Component }) => m.default

/** Every section, in the order of the section list (`SETTINGS_SECTIONS_IN_ORDER`). */
export const SETTINGS_SECTIONS: readonly SettingsSectionDef[] = [
  {
    id: 'projects',
    label: 'Projects',
    description: 'Workspace folders and the repositories found in them',
    component: () => import('./projects/ProjectsPage.vue').then(page),
  },
  {
    id: 'agents',
    label: 'Agents',
    description: 'Agent programs, their accounts and models',
    component: () => import('./agents/AgentsPage.vue').then(page),
  },
  { id: 'roles', label: 'Roles', description: 'Which models each role works with', component: () => import('./roles/RolesPage.vue').then(page) },
  {
    id: 'usage',
    label: 'Usage',
    description: 'Usage scripts and the limits that keep agents off busy models',
    component: () => import('./usage/UsagePage.vue').then(page),
  },
  {
    id: 'prompts',
    label: 'Prompts',
    description: 'Your instructions and the prompts agents get',
    component: () => import('./prompts/PromptsPage.vue').then(page),
  },
  {
    id: 'skills',
    label: 'Skill sets',
    description: 'Skill repositories agents are pointed at',
    component: () => import('./skills/SkillSetsPage.vue').then(page),
  },
  {
    id: 'environment',
    label: 'Environment',
    description: 'Variables every agent starts with',
    component: () => import('./environment/EnvironmentPage.vue').then(page),
  },
  {
    id: 'limits',
    label: 'Limits',
    description: 'Running agents, health checks and time limits',
    component: () => import('./limits/LimitsPage.vue').then(page),
  },
  {
    id: 'review',
    label: 'Review',
    description: 'Review and release defaults for new tickets',
    component: () => import('./review/ReviewPage.vue').then(page),
  },
  {
    id: 'plugins',
    label: 'Plugins',
    description: 'Plugins that react to the board',
    component: () => import('./plugins/PluginsPage.vue').then(page),
  },
  { id: 'general', label: 'General', description: 'The CEO, merge defaults, this server and the theme', component: () => import('./general/GeneralPage.vue').then(page) },
]

/** The section `id`. */
export function sectionDef(id: SettingsSection): SettingsSectionDef {
  return SETTINGS_SECTIONS.find((s) => s.id === id)!
}

/** Provided by the settings view: true below 768 px, where the section list and a page are shown one at a time. */
export const SETTINGS_NARROW: InjectionKey<Readonly<Ref<boolean>>> = Symbol('settings-narrow')

/** Provided by the settings view: a notice for the top of the page shown (an address that named no section). */
export const SETTINGS_NOTICE: InjectionKey<Readonly<Ref<string | null>>> = Symbol('settings-notice')

/** Provided by the settings view: set when the page shown should take the focus on its title (a narrow screen moved from the list to it). */
export const SETTINGS_FOCUS_TITLE: InjectionKey<Ref<boolean>> = Symbol('settings-focus-title')

/**
 * The page an address of the settings shows: the section it names, Projects
 * for an address naming no section, and on a wide screen for /settings
 * itself; null where a narrow screen shows the section list alone.
 */
export function shownSection(param: unknown, narrow: boolean): SettingsSection | null {
  const p = Array.isArray(param) ? param[0] : param
  if (typeof p !== 'string' || p === '') return narrow ? null : 'projects'
  return isSettingsSection(p) ? p : 'projects'
}

/** The layout the settings view shows now, kept by the view; read by the leave guard to tell whether a navigation changes the page shown. */
export const settingsLayout = { narrow: false }
