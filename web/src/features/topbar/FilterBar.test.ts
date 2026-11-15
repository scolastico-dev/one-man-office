import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { createMemoryHistory } from 'vue-router'
import App from '../../App.vue'
import { createTokenStore } from '../../auth/token'
import { createAppRouter } from '../../router'
import { createSession, SESSION } from '../../session'
import { FakeOmo, MemoryStorage, project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

async function boot(path = '/') {
  const omo = new FakeOmo()
  omo.projects = [project('ui', 'not-three/ui')]
  omo.tickets = [ticket({ number: 1, type: 'story', title: 'Story one' }), ticket({ number: 2, title: 'Task two', story: 1 })]
  const router = createAppRouter({ history: createMemoryHistory() })
  await router.push(path)
  await router.isReady()
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient: omo.makeClient, connect: omo.connect })
  const wrapper = mount(App, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: session } } })
  mounted = wrapper
  await flushPromises()
  return { wrapper, router }
}

test('every filter control has an accessible name', async () => {
  const { wrapper } = await boot()
  const form = wrapper.get('form[role=search]')
  const names = form.findAll('select, input, button').map((el) => {
    const label = el.element.closest('label')?.textContent ?? ''
    return (el.attributes('aria-label') ?? label ?? el.text()).trim() || el.text().trim()
  })
  expect(names.length).toBeGreaterThanOrEqual(6)
  for (const n of names) expect(n).not.toBe('')
  expect(form.text()).toContain('Project')
  expect(form.text()).toContain('Supervisor')
})

test('"/" focuses the search field, but not while typing elsewhere', async () => {
  const { wrapper } = await boot()
  const search = wrapper.get('input[type=search]').element as HTMLInputElement
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true }))
  expect(document.activeElement).toBe(search)

  search.blur()
  const select = wrapper.findAll('select')[0]!.element as HTMLSelectElement
  select.focus()
  select.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true }))
  expect(document.activeElement).toBe(select)
})

test('Escape in the search field clears the text and the filter', async () => {
  const { wrapper, router } = await boot('/?q=two')
  const input = wrapper.get('input[type=search]')
  expect((input.element as HTMLInputElement).value).toBe('two')
  await input.trigger('keydown', { key: 'Escape' })
  await flushPromises()
  expect((input.element as HTMLInputElement).value).toBe('')
  expect(router.currentRoute.value.query).toEqual({})
})

test('the active filter count shows on the Filters button and the sheet holds the same controls', async () => {
  const { wrapper, router } = await boot('/?needs_me=1&project=ui')
  const button = wrapper.findAll('button').find((b) => b.text().startsWith('Filters'))!
  expect(button.text()).toBe('Filters (2)')
  await button.trigger('click')
  await flushPromises()
  const dialog = wrapper.findAll('dialog').find((d) => d.text().includes('Filters'))!
  expect(dialog.text()).toContain('Filters')
  expect(dialog.findAll('select').length).toBe(3)
  const clear = dialog.findAll('button').find((b) => b.text() === 'Clear filters')!
  await clear.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({})
  expect(button.text()).toBe('Filters')
})

test('an active story is a removable chip', async () => {
  const { wrapper, router } = await boot('/?story=1')
  const chip = wrapper.get('[data-story-chip]')
  expect(chip.text()).toContain('Story #1 Story one')
  await chip.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.query).toEqual({})
  expect(wrapper.find('[data-story-chip]').exists()).toBe(false)
})
