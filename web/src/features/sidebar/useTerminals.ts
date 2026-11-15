// The user's shells, shared by the Terminals tab and the tab's count.
import { computed, shallowRef, type ComputedRef, type Ref } from 'vue'
import type { UserTerminal } from '../../api/types'

export interface Terminals {
  terminals: Readonly<Ref<UserTerminal[]>>
  /** Why the list could not be read, in the server's words; empty when it could. */
  error: Readonly<Ref<string>>
  count: ComputedRef<number>
}

const terminals = shallowRef<UserTerminal[]>([])
const error = shallowRef('')

/** The user's shells; they are listed by the Terminals tab once it is built. */
export function useTerminals(): Terminals {
  return { terminals, error, count: computed(() => terminals.value.length) }
}
