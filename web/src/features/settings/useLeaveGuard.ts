// Questions a settings page asks before something is lost: leaving it with
// unsaved changes, or saving over a file that cannot be read. The question is
// shown by `QuestionHost` (mounted once by the settings view) in a dialog;
// without a host the browser's own confirm asks.
import { defineComponent, getCurrentScope, h, onBeforeUnmount, onMounted, onScopeDispose, shallowRef, watch, type Ref } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, type RouteLocationNormalized } from 'vue-router'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'

export interface Question {
  /** The dialog's title: the question itself. */
  title: string
  /** What happens when the user agrees. */
  text: string
  /** The button that agrees. */
  confirm: string
  /** The button that keeps things as they are. */
  cancel: string
}

/** Asked before a page with unsaved changes is left. */
export const LEAVE_QUESTION: Question = {
  title: 'Leave without saving?',
  text: 'Your changes are lost.',
  confirm: 'Leave',
  cancel: 'Stay',
}

interface Asked {
  question: Question
  answer(ok: boolean): void
}

const asked = shallowRef<Asked | null>(null)
let hosts = 0

/**
 * Asks `question` and resolves with true when the user agrees. A question
 * asked while another is open answers the older one with false. Without a
 * mounted `QuestionHost` the browser's confirm asks instead.
 */
export function askFirst(question: Question): Promise<boolean> {
  if (hosts === 0) return Promise.resolve(window.confirm(`${question.title}\n\n${question.text}`))
  asked.value?.answer(false)
  return new Promise((resolve) => {
    let done = false
    const entry: Asked = {
      question,
      answer(ok) {
        if (done) return
        done = true
        if (asked.value === entry) asked.value = null
        resolve(ok)
      },
    }
    asked.value = entry
  })
}

/** Shows the question `askFirst` asks, as a dialog. Mount it once. */
export const QuestionHost = defineComponent({
  name: 'QuestionHost',
  setup() {
    onMounted(() => hosts++)
    onBeforeUnmount(() => {
      hosts--
      asked.value?.answer(false)
    })
    return () => {
      const a = asked.value
      if (a === null) return null
      return h(
        UiDialog,
        { open: true, title: a.question.title, onClose: () => a.answer(false), 'data-question': '' },
        {
          default: () => h('p', { class: 'm-0' }, a.question.text),
          actions: () => [
            h(UiButton, { onClick: () => a.answer(false), 'data-question-cancel': '' }, () => a.question.cancel),
            h(UiButton, { variant: 'danger', onClick: () => a.answer(true), 'data-question-confirm': '' }, () => a.question.confirm),
          ],
        },
      )
    }
  },
})

/**
 * One answer per navigation: every guarded part of a page asks through the
 * same navigation target, so several dirty forms ask once.
 */
const answers = new WeakMap<RouteLocationNormalized, Promise<boolean>>()

function askToLeave(to: RouteLocationNormalized): Promise<boolean> {
  let answer = answers.get(to)
  if (answer === undefined) {
    answer = askFirst(LEAVE_QUESTION)
    answers.set(to, answer)
  }
  return answer
}

function onBeforeUnload(event: BeforeUnloadEvent) {
  event.preventDefault()
  // Older browsers show their question only when returnValue is set.
  event.returnValue = ''
}

/**
 * While `dirty` is true, leaving the page asks first: a navigation to another
 * address (another settings section, the board, a ticket) through
 * `LEAVE_QUESTION`, closing or reloading the tab through the browser's own
 * question. Call it in the setup of a component rendered inside the router's
 * view; several calls on one page ask once per navigation.
 */
export function useLeaveGuard(dirty: Ref<boolean>): void {
  const guard = (to: RouteLocationNormalized, from: RouteLocationNormalized) => {
    if (!dirty.value || to.path === from.path) return true
    return askToLeave(to)
  }
  onBeforeRouteLeave(guard)
  onBeforeRouteUpdate(guard)

  let listening = false
  const listen = (on: boolean) => {
    if (on === listening) return
    listening = on
    if (on) window.addEventListener('beforeunload', onBeforeUnload)
    else window.removeEventListener('beforeunload', onBeforeUnload)
  }
  watch(dirty, (d) => listen(d), { immediate: true })
  if (getCurrentScope()) onScopeDispose(() => listen(false))
}
