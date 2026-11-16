import { defineComponent, h, inject, ref, type Component } from 'vue'
import { lazyComponent } from '../../components/ui/chunkFailure'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'
import { usePopouts } from './popout'
import TerminalOut from './TerminalOut.vue'
import { targetOfSrc } from './terminalTarget'

/** The terminal view itself, fetched with xterm.js the first time one is shown. */
const AsyncTerminal = lazyComponent(() => import('./TerminalView.vue'), {
  delay: 400,
  loadingComponent: { render: () => h('p', { class: 'text-muted text-xs', role: 'status' }, 'Loading the terminal') },
})

/**
 * `inner` rendered only while the board shows: while it waits under the lock
 * screen the component is unmounted (a terminal closes its connection) and
 * mounted again afterwards. While the terminal (named by the `src` prop) is
 * open in its own window, `TerminalOut` stands in its place, so this window
 * holds no connection to it. Passes props, slots and events on and exposes
 * `focus()`.
 */
export function whileBoardShown(inner: Component, name: string) {
  return defineComponent({
    name,
    inheritAttrs: false,
    setup(_, { attrs, slots, expose }) {
      const suspended = inject(BOARD_SUSPENDED, ref(false))
      const el = ref<{ focus?: () => void } | null>(null)
      expose({ focus: () => el.value?.focus?.() })
      return () => {
        if (suspended.value) return null
        // The terminal's own window (`popout: false`) is the one view that stays.
        const target = attrs.popout !== false && typeof attrs.src === 'string' ? targetOfSrc(attrs.src) : null
        if (target && usePopouts().stateOf(target) === 'out') {
          return h(TerminalOut, { target, label: String(attrs.label ?? 'Terminal'), fill: attrs.fill !== undefined && attrs.fill !== false })
        }
        return h(inner, { ...attrs, ref: el }, slots)
      }
    },
  })
}

/**
 * The terminal view, fetched with xterm.js the first time one is shown. Every
 * other file uses this, never `TerminalView.vue` itself, so the emulator stays
 * out of the entry chunk. It connects again with the new token after a lock.
 */
export const LazyTerminal = whileBoardShown(AsyncTerminal, 'LazyTerminal')
