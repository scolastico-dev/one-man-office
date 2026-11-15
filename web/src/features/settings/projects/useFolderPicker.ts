// Opens the folder picker from code and waits for the answer:
//
//   const picker = useFolderPicker()
//   const path = await picker.choose({ title: 'Choose a workspace folder' })
//
// The component that calls it renders the dialog once:
//
//   <FolderPickerDialog v-bind="picker.dialog.value" @choose="picker.chosen" @close="picker.closed" />
import { computed, reactive, type ComputedRef } from 'vue'

export interface FolderPickerRequest {
  /** The dialog's title, e.g. "Choose a workspace folder". */
  title: string
  /** The folder shown first; the home folder when empty. */
  start?: string
}

export interface FolderPicker {
  /** The props of the dialog. */
  dialog: ComputedRef<{ open: boolean; title: string; start?: string }>
  /** Opens the picker; resolves with the exact path chosen, or null when it was closed. A second call closes the first with null. */
  choose(req: FolderPickerRequest): Promise<string | null>
  /** The dialog's `choose`. */
  chosen(path: string): void
  /** The dialog's `close`. */
  closed(): void
}

export function useFolderPicker(): FolderPicker {
  const state = reactive<{ open: boolean; title: string; start: string | undefined }>({ open: false, title: '', start: undefined })
  let answer: ((path: string | null) => void) | null = null

  function finish(path: string | null) {
    state.open = false
    const a = answer
    answer = null
    a?.(path)
  }

  return {
    dialog: computed(() => (state.start === undefined ? { open: state.open, title: state.title } : { open: state.open, title: state.title, start: state.start })),
    choose(req) {
      finish(null)
      state.title = req.title
      state.start = req.start === undefined || req.start.trim() === '' ? undefined : req.start.trim()
      state.open = true
      return new Promise((resolve) => (answer = resolve))
    },
    chosen: (path) => finish(path),
    closed: () => finish(null),
  }
}
