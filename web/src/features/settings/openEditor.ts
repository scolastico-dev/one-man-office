// The one open editor of a settings page that lists items (usage scripts,
// skill sets, plugins). Switching to another item, or closing the open one,
// asks first while it has unsaved edits, and is refused while omo is still
// working on it (a test, an update, a removal): the editor stays where the
// user started the work, so its answer is seen in place. The refusal says so
// next to the editor and is announced (once: the note is no live region). When an editor goes away by itself (deleted, removed, a
// new item closed), focus goes to its row's button, or to the button that
// adds an item when the row is gone, never to the page.
import { nextTick, ref, watch, type Ref } from 'vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
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
  const announce = useAnnouncer()
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
        announce.assertive(busyNote.value)
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

/** After an editor went away: focus its row's button when the row is still listed, else the button that adds an item. */
export async function focusAfterClose(row: () => HTMLElement | null | undefined, add: () => HTMLElement | null | undefined): Promise<void> {
  await nextTick()
  ;(row() ?? add())?.focus()
}

/** The button of a list row, found by the row's data attribute and the button's (`[data-script="x"] [data-script-toggle]`). */
export function rowButton(rowAttr: string, name: string, buttonAttr: string): HTMLElement | null {
  return document.querySelector<HTMLElement>(`[${rowAttr}="${CSS.escape(name)}"] [${buttonAttr}]`)
}
