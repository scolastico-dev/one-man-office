import { expect, test } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { leavePanel } from './sidebarActions'

function router(stay: { value: boolean }) {
  const r = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/:n(\\d+)', name: 'ticket', component: { render: () => null }, beforeEnter: () => true },
      { path: '/:p(.*)*', component: { render: () => null } },
    ],
  })
  r.beforeEach((_to, from) => (from.name === 'ticket' && stay.value ? false : true))
  return r
}

test('leaving the panel says whether the panel is gone; a refused navigation keeps it', async () => {
  const stay = { value: true }
  const r = router(stay)
  await r.push('/12?tab=agents&comment=3&q=x')
  expect(await leavePanel(r)).toBe(false)
  expect(r.currentRoute.value.path).toBe('/12')
  stay.value = false
  expect(await leavePanel(r)).toBe(true)
  expect(r.currentRoute.value.fullPath).toBe('/?q=x')
  // Already on the board: nothing to leave.
  expect(await leavePanel(r)).toBe(true)
})
