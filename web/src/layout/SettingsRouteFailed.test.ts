import { flushPromises, mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { CHUNK_FAILED_TEXT, FAILED_PAGE_TITLE, lazyRoute } from '../components/ui/chunkFailure'
import SettingsRouteFailed from './SettingsRouteFailed.vue'

test('the settings route, when its code cannot be fetched, shows the message in the settings frame under a page title', async () => {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/settings', component: lazyRoute(() => Promise.reject(new TypeError('Failed to fetch')), SettingsRouteFailed) }],
  })
  await router.push('/settings')
  const w = mount(defineComponent({ setup: () => () => h(RouterView) }), { global: { plugins: [router] } })
  await flushPromises()
  const failed = w.get('[data-route-failed]')
  expect(failed.classes()).toContain('settings-frame')
  expect(failed.get('h1').text()).toBe(FAILED_PAGE_TITLE)
  expect(failed.text()).toContain(CHUNK_FAILED_TEXT)
  w.unmount()
})
