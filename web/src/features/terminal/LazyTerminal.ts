import { defineComponent, getCurrentInstance, h, inject, onBeforeUnmount, onMounted, onUpdated, ref, type Component } from 'vue'
import { lazyComponent } from '../../components/ui/chunkFailure'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'
import { usePopouts } from './popout'
import TerminalOut from './TerminalOut.vue'
import { keyOf, targetOfSrc } from './terminalTarget'
import { placeOf, useTerminalViewers } from './viewers'

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
 * holds no connection to it. Within the window one view of a terminal is
 * live, the one opened last (`viewers.ts`); another view of it shows
 * `TerminalOut` saying where the terminal is, with "Show it here". Passes
 * props, slots and events on and exposes `focus()`.
 */
export function whileBoardShown(inner: Component, name: string) {
  return defineComponent({
    name,
    inheritAttrs: false,
    setup(_, { attrs, slots, expose }) {
      const suspended = inject(BOARD_SUSPENDED, ref(false))
      const el = ref<{ focus?: () => void } | null>(null)
      expose({ focus: () => el.value?.focus?.() })
      const viewers = useTerminalViewers()
      const self = getCurrentInstance()
      const targetOf = () => (typeof attrs.src === 'string' ? targetOfSrc(attrs.src) : null)

      // This view's claim on its terminal (src changes only with a new key in practice; followed anyway).
      let claim: { key: string; id: number } | null = null
      const where = () => {
        const node = self?.proxy?.$el as Node | null | undefined
        return placeOf(node instanceof Element ? node : (node?.parentElement ?? null))
      }
      function follow() {
        const t = targetOf()
        const key = t ? keyOf(t) : null
        if (claim?.key === key) {
          if (claim) viewers.setPlace(claim.key, claim.id, where())
          return
        }
        if (claim) viewers.release(claim.key, claim.id)
        claim = key ? { key, id: viewers.claim(key, where()) } : null
      }
      follow()
      onMounted(follow)
      onUpdated(follow)
      onBeforeUnmount(() => {
        if (claim) viewers.release(claim.key, claim.id)
        claim = null
      })

      return () => {
        if (suspended.value) return null
        const fill = attrs.fill !== undefined && attrs.fill !== false
        const label = String(attrs.label ?? 'Terminal')
        const target = targetOf()
        // The terminal's own window (`popout: false`) is the one view that stays.
        if (target && attrs.popout !== false && usePopouts().stateOf(target) === 'out') {
          return h(TerminalOut, { target, label, fill })
        }
        const live = claim ? viewers.live(claim.key) : null
        if (target && claim && live && live.id !== claim.id) {
          const mine = claim
          return h(TerminalOut, { target, label, fill, place: live.place, onTake: () => viewers.takeOver(mine.key, mine.id) })
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
