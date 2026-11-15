// The life of one field's edit: from the click that opens it to the value
// the stream confirms. One change is one request that carries only that
// field; the answer is never written to the store, so the saved value is
// shown until the stream has delivered it and then the stream's value wins.
import { computed, ref, shallowRef, type Ref } from 'vue'
import type { ApiResult } from '../../api/client'
import { updateTicket } from '../../api/endpoints'
import type { UpdateTicket } from '../../api/types'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { problemOf, type FieldProblem } from '../create/createRules'

const clone = <T>(v: T): T => (v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T))
const sameJson = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

export interface FieldEditOptions<T> {
  number: () => number
  /** The ticket's value now, as the board has it. */
  current: () => T
  /** The request body for a value; the edit sends nothing else. */
  patch: (value: T) => UpdateTicket
  /** The value in words, for announcements and for "changed meanwhile". */
  text: (value: T) => string
  /** Name of the field in announcements. */
  label: string
  equals?: (a: T, b: T) => boolean
  /** A message for a value that cannot be sent at all; the edit stays open. */
  validate?: (value: T) => string
}

export interface FieldEdit<T> {
  editing: Ref<boolean>
  draft: Ref<T>
  saving: Ref<boolean>
  /** What the server said about the last save, with the field it concerns. */
  problem: Ref<FieldProblem | null>
  error: Ref<string>
  /** The value to show: the saved one until the stream has caught up, else the board's. */
  shown: Ref<T>
  /** Set while the edit is open and the board's value differs from the one the edit began with. */
  changedNote: Ref<string>
  start(): void
  cancel(): void
  save(): Promise<void>
  /** Takes the board's newer value into the open edit. */
  useNew(): void
}

export function useFieldEdit<T>(o: FieldEditOptions<T>): FieldEdit<T> {
  const store = useBoard()
  const session = useSession()
  const say = useAnnouncer()
  const equals = o.equals ?? sameJson

  const editing = ref(false)
  const saving = ref(false)
  const draft = ref(clone(o.current())) as Ref<T>
  const base = shallowRef<T>(o.current())
  const problem = ref<FieldProblem | null>(null)
  const awaiting = shallowRef<{ value: T; eventId: number } | null>(null)

  const shown = computed(() => {
    const a = awaiting.value
    return (a !== null && store.state.lastEventId < a.eventId ? a.value : o.current()) as T
  })
  const changedNote = computed(() => {
    if (!editing.value || saving.value || equals(o.current(), base.value)) return ''
    return `This changed while you were editing. It is now ${o.text(o.current())}.`
  })
  const error = computed(() => problem.value?.message ?? '')

  function start() {
    base.value = clone(shown.value)
    draft.value = clone(shown.value)
    problem.value = null
    editing.value = true
  }

  function cancel() {
    if (saving.value) return
    editing.value = false
    problem.value = null
  }

  function useNew() {
    base.value = clone(o.current())
    draft.value = clone(o.current())
    problem.value = null
  }

  async function save() {
    if (saving.value) return
    const value = clone(draft.value)
    // Unchanged means unchanged from what the board shows now, not from what the edit began with:
    // after a change from elsewhere, keeping my own value is a change.
    if (equals(value, shown.value)) {
      cancel()
      return
    }
    const unsendable = o.validate?.(value) ?? ''
    if (unsendable !== '') {
      problem.value = { field: null, message: unsendable }
      say.assertive(unsendable)
      return
    }
    saving.value = true
    problem.value = null
    try {
      const res: ApiResult<unknown> = await updateTicket(session.client, o.number(), o.patch(value))
      if (res.eventId !== null && store.state.lastEventId < res.eventId) awaiting.value = { value, eventId: res.eventId }
      editing.value = false
      say.polite(`${o.label} of #${o.number()} saved.`)
    } catch (e) {
      // The previous value comes back: nothing was written here, and the message stays at the field.
      editing.value = false
      problem.value = problemOf(e, `omo did not accept the ${o.label.toLowerCase()}.`)
      say.assertive(problem.value.message)
    } finally {
      saving.value = false
    }
  }

  return { editing, draft, saving, problem, error, shown, changedNote, start, cancel, save, useNew }
}
