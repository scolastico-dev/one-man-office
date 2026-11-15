// Dialogs opened from code (an action that asks first, an answer shown over
// the panel) rather than written into a template. They are rendered by a
// DialogOutlet inside the component that owns them, so they get everything
// that component's tree provides (the session, the board, BOARD_SUSPENDED:
// under the lock screen they step aside like every other dialog), and they
// are closed when their owner goes away (a ticket panel that closes takes
// its dialogs with it). This is the only way to show such a dialog; a
// separate app (`createApp`) would have none of these.
import { defineComponent, getCurrentScope, h, markRaw, onScopeDispose, shallowReactive, type PropType, type VNodeChild } from 'vue'

/** Draws a dialog; `close` takes it away (and tells its opener). */
export type DialogRender = (close: () => void) => VNodeChild

interface Entry {
  id: number
  render: DialogRender
  close: () => void
}

/** The dialogs of one owner. */
export interface DialogScope {
  /** The dialogs open now, oldest first; a DialogOutlet renders them. */
  readonly open: readonly Readonly<Entry>[]
  /** Closes every dialog of the scope, as if each was closed by the user. */
  closeAll(): void
}

interface ScopeInternals extends DialogScope {
  entries: Entry[]
}

let nextId = 1

/** A scope without an owner; the caller closes it. Exported for tests. */
export function createDialogScope(): DialogScope {
  const entries = shallowReactive<Entry[]>([])
  const scope: ScopeInternals = {
    entries,
    get open() {
      return entries
    },
    closeAll() {
      for (const e of [...entries]) e.close()
    },
  }
  return scope
}

/** A scope whose dialogs close when the calling component (or effect scope) goes away. */
export function useDialogScope(): DialogScope {
  const scope = createDialogScope()
  if (getCurrentScope()) onScopeDispose(() => scope.closeAll())
  return scope
}

/**
 * Shows a dialog in `scope` until `close` is called: by the dialog itself,
 * by the returned function, or by the scope going away. `onClosed` runs once,
 * whichever came first, so an opener waiting for an answer is never left
 * waiting.
 */
export function mountDialog(render: DialogRender, scope: DialogScope, onClosed?: () => void): () => void {
  const { entries } = scope as ScopeInternals
  let closed = false
  const entry: Entry = markRaw({
    id: nextId++,
    render,
    close: () => {
      if (closed) return
      closed = true
      const i = entries.indexOf(entry)
      if (i >= 0) entries.splice(i, 1)
      onClosed?.()
    },
  })
  entries.push(entry)
  return entry.close
}

// One dialog, as its own component, so its state lives as long as it is open.
const DialogSlot = defineComponent({
  name: 'DialogSlot',
  props: { entry: { type: Object as PropType<Entry>, required: true } },
  setup(props) {
    return () => props.entry.render(props.entry.close)
  },
})

/** Renders the dialogs of a scope where it is placed; place it in the owner's template. */
export const DialogOutlet = defineComponent({
  name: 'DialogOutlet',
  props: { scope: { type: Object as PropType<DialogScope>, required: true } },
  setup(props) {
    return () => props.scope.open.map((e) => h(DialogSlot, { key: e.id, entry: e }))
  },
})
