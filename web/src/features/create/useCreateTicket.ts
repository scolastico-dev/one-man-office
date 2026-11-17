import { reactive, readonly } from 'vue'

const state = reactive<{ open: boolean; initialStory: number | undefined }>({ open: false, initialStory: undefined })

/**
 * Whether the "Create ticket" dialog is open, shared by everything that opens
 * it (the top bar button, the `c` key, a column's add button) and the board
 * page that renders the dialog.
 */
export function useCreateTicket() {
  return {
    state: readonly(state),
    /** Opens the dialog; `initialStory` presets the story of a new task. */
    open(initialStory?: number) {
      state.initialStory = initialStory
      state.open = true
    },
    close() {
      state.open = false
      state.initialStory = undefined
    },
  }
}
