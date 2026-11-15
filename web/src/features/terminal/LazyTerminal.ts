import { defineComponent, h, inject, ref, type Component } from 'vue'
import { lazyComponent } from '../../components/ui/chunkFailure'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'

/** The terminal view itself, fetched with xterm.js the first time one is shown. */
const AsyncTerminal = lazyComponent(() => import('./TerminalView.vue'), {
  delay: 400,
  loadingComponent: { render: () => h('p', { class: 'text-muted text-xs', role: 'status' }, 'Loading the terminal') },
})

/**
 * `inner` rendered only while the board shows: while it waits under the lock
 * screen the component is unmounted (a terminal closes its connection) and
 * mounted again afterwards. Passes props and events on and exposes `focus()`.
 */
export function whileBoardShown(inner: Component, name: string) {
  return defineComponent({
    name,
    inheritAttrs: false,
    setup(_, { attrs, expose }) {
      const suspended = inject(BOARD_SUSPENDED, ref(false))
      const el = ref<{ focus?: () => void } | null>(null)
      expose({ focus: () => el.value?.focus?.() })
      return () => (suspended.value ? null : h(inner, { ...attrs, ref: el }))
    },
  })
}

/**
 * The terminal view, fetched with xterm.js the first time one is shown. Every
 * other file uses this, never `TerminalView.vue` itself, so the emulator stays
 * out of the entry chunk. It connects again with the new token after a lock.
 */
export const LazyTerminal = whileBoardShown(AsyncTerminal, 'LazyTerminal')
