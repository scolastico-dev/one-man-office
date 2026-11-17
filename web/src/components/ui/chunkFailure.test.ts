import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { CHUNK_FAILED_TEXT, ChunkFailedDialog, FAILED_PAGE_TITLE, lazyComponent, lazyRoute, pageReload } from './chunkFailure'
import { DIALOGS_IN_PLACE } from './dialogHost'

const reload = pageReload.run
afterEach(() => {
  pageReload.run = reload
})

const failing = () => Promise.reject(new TypeError('Failed to fetch dynamically imported module: /assets/SettingsView-old.js'))

test('a lazy route whose code cannot be fetched ends on a page that says so, with Reload', async () => {
  const reloaded = vi.fn()
  pageReload.run = reloaded
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { render: () => h('p', 'board') } },
      { path: '/settings', component: lazyRoute(failing as () => Promise<{ default: never }>) },
    ],
  })
  await router.push('/')
  const w = mount(defineComponent({ setup: () => () => h(RouterView) }), { global: { plugins: [router] } })
  await router.push('/settings')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/settings')
  expect(w.get('[data-route-failed]').text()).toContain(CHUNK_FAILED_TEXT)
  expect(w.find('[role="alert"]').exists()).toBe(true)
  expect(w.get('[data-route-failed] h1').text()).toBe(FAILED_PAGE_TITLE)
  await w.get('[data-chunk-reload]').trigger('click')
  expect(reloaded).toHaveBeenCalledOnce()
  w.unmount()
})

test('a lazy component whose code cannot be fetched shows the message in its place; a dialog shows it as a dialog', async () => {
  const Part = lazyComponent(failing)
  const w = mount(defineComponent({ setup: () => () => h(Part) }))
  await flushPromises()
  await flushPromises()
  expect(w.get('[data-chunk-failed]').text()).toContain(CHUNK_FAILED_TEXT)
  w.unmount()

  const Picker = lazyComponent(failing, { errorComponent: ChunkFailedDialog })
  const d = mount(defineComponent({ setup: () => () => h(Picker) }), { attachTo: document.body, global: { provide: { [DIALOGS_IN_PLACE as symbol]: true } } })
  await flushPromises()
  await flushPromises()
  expect((d.get('dialog').element as HTMLDialogElement).open).toBe(true)
  expect(d.get('dialog').text()).toContain(CHUNK_FAILED_TEXT)
  await d.findAll('button').find((b) => b.text() === 'Close')!.trigger('click')
  expect((d.get('dialog').element as HTMLDialogElement).open).toBe(false)
  d.unmount()
})
