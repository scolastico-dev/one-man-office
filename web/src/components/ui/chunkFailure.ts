// Parts of the app are fetched when first shown (the settings and their
// pages, the terminal, some sidebar tabs, the folder picker). Such a fetch
// fails when omo was updated while the tab stayed open (the old page asks for
// files the new omo no longer has) or when the connection drops at that
// moment. Every lazy part goes through `lazyComponent` or `lazyRoute`, so a
// failure shows a message with Reload instead of nothing; a guard test keeps
// it that way. The message parts are in the entry chunk, never fetched.
import { defineAsyncComponent, defineComponent, h, ref, type AsyncComponentLoader, type AsyncComponentOptions, type Component } from 'vue'
import Callout from './Callout.vue'
import UiButton from './UiButton.vue'
import UiDialog from './UiDialog.vue'

/** What a part that could not be loaded says. */
export const CHUNK_FAILED_TEXT =
  'This part of omo could not be loaded, perhaps because omo was updated while this tab stayed open. Reload the page to get the current version.'

/** Reloads the page; replaced in tests. */
export const pageReload = { run: (): void => window.location.reload() }

const reloadButton = (size?: 'sm') =>
  h(UiButton, { size, variant: size ? undefined : 'primary', 'data-chunk-reload': '', onClick: () => pageReload.run() }, () => 'Reload')

/** The message with Reload, in place of the part that could not be loaded. */
export const ChunkFailed = defineComponent({
  name: 'ChunkFailed',
  setup: () => () =>
    h(Callout, { tone: 'danger', live: 'assertive', 'data-chunk-failed': '' }, { default: () => h('p', CHUNK_FAILED_TEXT), actions: () => reloadButton('sm') }),
})

/** The message with Reload as a dialog of its own, for a dialog that could not be loaded; Close closes it. */
export const ChunkFailedDialog = defineComponent({
  name: 'ChunkFailedDialog',
  setup() {
    const open = ref(true)
    return () =>
      h(
        UiDialog,
        { open: open.value, title: 'This could not be loaded', onClose: () => (open.value = false) },
        {
          default: () => h('p', { class: 'm-0', 'data-chunk-failed': '' }, CHUNK_FAILED_TEXT),
          actions: () => [h(UiButton, { onClick: () => (open.value = false) }, () => 'Close'), reloadButton()],
        },
      )
  },
})

/** An async component that shows the message with Reload (or `errorComponent`) when its code cannot be fetched. */
export function lazyComponent(loader: AsyncComponentLoader, o: Omit<AsyncComponentOptions, 'loader'> = {}): Component {
  return defineAsyncComponent({ errorComponent: ChunkFailed, ...o, loader })
}

/** The name of a page that could not be loaded; a page without a level-one heading fails the accessibility check. */
export const FAILED_PAGE_TITLE = 'This page could not be loaded'

/** Shown by the router in place of a page whose code could not be fetched. */
export const RouteFailed = defineComponent({
  name: 'RouteFailed',
  setup: () => () => h('section', { class: 'route-failed', 'data-route-failed': '' }, [h('h1', { class: 'sr-only' }, FAILED_PAGE_TITLE), h(ChunkFailed)]),
})

/** A route's lazy component: resolves to `RouteFailed` when its code cannot be fetched, so the navigation still ends on a page. */
export function lazyRoute(loader: () => Promise<{ default: Component }>): () => Promise<Component> {
  return () =>
    loader().then(
      (m) => m.default,
      () => RouteFailed,
    )
}
