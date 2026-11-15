import { defineAsyncComponent, h } from 'vue'

/**
 * The terminal view, fetched with xterm.js the first time one is shown. Every
 * other file uses this, never `TerminalView.vue` itself, so the emulator stays
 * out of the entry chunk.
 */
export const LazyTerminal = defineAsyncComponent({
  loader: () => import('./TerminalView.vue'),
  delay: 400,
  loadingComponent: { render: () => h('p', { class: 'text-muted text-xs', role: 'status' }, 'Loading the terminal') },
  errorComponent: {
    render: () => h('p', { class: 'text-xs', role: 'alert' }, 'The terminal could not be loaded. Reload the page to try again.'),
  },
})
