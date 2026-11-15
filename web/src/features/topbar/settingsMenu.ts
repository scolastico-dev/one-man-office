import { reactive, readonly } from 'vue'
import type { Router } from 'vue-router'
import { settingsLocation } from '../../locations'

/** One entry of the gear menu in the top bar. */
export interface MenuEntry {
  id: string
  label: string
  open(): void
}

/** The dialogs the gear menu and the first steps open over the board. */
export type SettingsDialog = 'addProject' | 'theme'

const state = reactive<{ open: SettingsDialog | null }>({ open: null })

/**
 * Which of these dialogs is open. Other parts of the app (the first steps)
 * open them through `open`; `SettingsMenu` renders the theme dialog and
 * `FirstSteps` the one that adds a project.
 */
export function useSettingsDialogs() {
  return {
    state: readonly(state),
    open(dialog: SettingsDialog) {
      state.open = dialog
    },
    close() {
      state.open = null
    },
  }
}

/**
 * The entries of the gear menu, in order: Settings (the Projects page),
 * Projects, Agent setup (the Roles page) and Theme (a dialog). The pages keep
 * the board's filters, so Back to board finds the board as it was.
 */
export function settingsMenu(router: Router): MenuEntry[] {
  const dialogs = useSettingsDialogs()
  const go = (section: 'projects' | 'roles') => () => void router.push(settingsLocation(section, router.currentRoute.value))
  return [
    { id: 'settings', label: 'Settings', open: go('projects') },
    { id: 'projects', label: 'Projects', open: go('projects') },
    { id: 'agents', label: 'Agent setup', open: go('roles') },
    { id: 'theme', label: 'Theme', open: () => dialogs.open('theme') },
  ]
}
