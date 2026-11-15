// The one open editor of a settings page that lists items (usage scripts,
// skill sets, plugins). Switching to another item, or closing the open one,
// asks first while it has unsaved edits, and is refused while omo is still
// working on it (a test, an update, a removal): the editor stays where the
// user started the work, so its answer is seen in place. The refusal says so
// next to the editor.
import { ref, watch, type Ref } from 'vue'
import { LEAVE_QUESTION, askFirst } from './useLeaveGuard'

export interface OpenEditor {
  /** The editor has unsaved edits (it reports them). */
  dirty: Ref<boolean>
  /** omo is working on the open item (the editor reports it). */
  busy: Ref<boolean>
  /** Why the editor did not close; empty when it did or nothing was tried. */
  busyNote: Ref<string>
  /** Whether the open editor may go: false while it is busy (with a note), or when the user keeps unsaved edits. */
  leave(): Promise<boolean>
  /** The editor went away by itself (its item was deleted): nothing is kept of it. */
  reset(): void
}

/** The text shown when the open editor cannot close yet. */
export const busyNoteOf = (what: string) => `omo is still working on ${what}; it stays open until omo has answered.`

export function useOpenEditor(what: () => string): OpenEditor {
  const dirty = ref(false)
  const busy = ref(false)
  const busyNote = ref('')
  watch(busy, (b) => {
    if (!b) busyNote.value = ''
  })
  return {
    dirty,
    busy,
    busyNote,
    async leave() {
      if (busy.value) {
        busyNote.value = busyNoteOf(what())
        return false
      }
      if (dirty.value && !(await askFirst(LEAVE_QUESTION))) return false
      dirty.value = false
      busyNote.value = ''
      return true
    },
    reset() {
      dirty.value = false
      busy.value = false
      busyNote.value = ''
    },
  }
}
