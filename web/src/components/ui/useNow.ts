// The time of day for words like "3 min ago" and "resets in 2 h": one clock
// for the whole page that ticks each minute while something uses it, instead
// of a timer per shown time (a long thread shows hundreds).
import { getCurrentScope, onScopeDispose, readonly, ref, type Ref } from 'vue'

const MINUTE = 60_000
const now = ref(Date.now())
let users = 0
let timer: ReturnType<typeof setInterval> | undefined

/**
 * Milliseconds since the epoch, updated each minute while the calling
 * component (or effect scope) lives. Outside one it is read once and does not
 * tick.
 */
export function useNow(): Readonly<Ref<number>> {
  if (!getCurrentScope()) return ref(Date.now())
  if (users++ === 0) timer = setInterval(() => (now.value = Date.now()), MINUTE)
  // A new user may come after a pause of the clock (nothing shown for a while).
  now.value = Date.now()
  onScopeDispose(() => {
    if (--users === 0) {
      clearInterval(timer)
      timer = undefined
    }
  })
  return readonly(now)
}
