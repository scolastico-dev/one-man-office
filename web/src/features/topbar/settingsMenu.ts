import { reactive, readonly } from 'vue'

/** One entry of the gear menu in the top bar. */
export interface MenuEntry {
  id: string
  label: string
  open(): void
}

/** The dialogs the gear menu can open. */
export type SettingsDialog = 'projects' | 'addProject' | 'theme' | 'agents'

const state = reactive<{ open: SettingsDialog | null }>({ open: null })

/**
 * Which gear-menu dialog is open. Other parts of the app (the first steps)
 * open the same dialogs through `open`, and `SettingsMenu` renders them.
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

/** The entries of the gear menu, in order: Projects, Theme, Agent setup. */
export function settingsMenu(): MenuEntry[] {
  const dialogs = useSettingsDialogs()
  return [
    { id: 'projects', label: 'Projects', open: () => dialogs.open('projects') },
    { id: 'theme', label: 'Theme', open: () => dialogs.open('theme') },
    { id: 'agents', label: 'Agent setup', open: () => dialogs.open('agents') },
  ]
}
