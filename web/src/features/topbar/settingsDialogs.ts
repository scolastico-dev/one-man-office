import { reactive, readonly } from 'vue'

/** The dialogs other parts of the app open over the board. */
export type SettingsDialog = 'addProject'

const state = reactive<{ open: SettingsDialog | null }>({ open: null })

/**
 * Which of these dialogs is open. The first steps open the one that adds a
 * project through `open`; `FirstSteps` renders it.
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
