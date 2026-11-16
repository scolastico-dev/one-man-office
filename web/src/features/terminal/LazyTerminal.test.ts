import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { defineComponent, h, provide, ref } from 'vue'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'
import { whileBoardShown } from './LazyTerminal'
import { resetPopouts, usePopouts } from './popout'

afterEach(() => {
  vi.restoreAllMocks()
  resetPopouts()
})

const Fake = defineComponent({
  props: { src: { type: String, required: true }, label: { type: String, required: true } },
  setup: (p) => () => h('p', { 'data-fake-terminal': p.src }, p.label),
})
const Wrapped = whileBoardShown(Fake, 'Wrapped')

test('the terminal is not rendered (its connection closes) while the board is suspended, and comes back after', async () => {
  const suspended = ref(false)
  const Host = defineComponent({
    setup() {
      provide(BOARD_SUSPENDED, suspended)
      return () => h(Wrapped, { src: '/api/terminals/x/socket', label: 'Terminal x' })
    },
  })
  const w = mount(Host)
  await flushPromises()
  expect(w.get('[data-fake-terminal]').attributes('data-fake-terminal')).toBe('/api/terminals/x/socket')
  suspended.value = true
  await flushPromises()
  expect(w.find('[data-fake-terminal]').exists()).toBe(false)
  suspended.value = false
  await flushPromises()
  expect(w.find('[data-fake-terminal]').text()).toBe('Terminal x')
  w.unmount()
})

test('while the terminal is open in its own window a placeholder stands in its place, with no terminal (no connection) mounted', async () => {
  const win = { closed: false, focus: vi.fn(), close: vi.fn(() => (win.closed = true)) }
  vi.spyOn(window, 'open').mockReturnValue(win as unknown as Window)
  const Host = defineComponent({
    setup() {
      provide(BOARD_SUSPENDED, ref(false))
      return () => [h(Wrapped, { src: '/api/agents/t1/terminal', label: 'Terminal of the CEO', fill: '' }), h(Wrapped, { src: '/api/terminals/s1/socket', label: 'Terminal Home' })]
    },
  })
  const w = mount(Host, { attachTo: document.body })
  await flushPromises()
  expect(w.findAll('[data-fake-terminal]')).toHaveLength(2)

  usePopouts().open({ kind: 'agent', id: 't1' }, 'ceo')
  await flushPromises()
  expect(w.findAll('[data-fake-terminal]').map((x) => x.attributes('data-fake-terminal'))).toEqual(['/api/terminals/s1/socket'])
  const out = w.get('[data-terminal-out]')
  expect(out.attributes('aria-label')).toBe('Terminal of the CEO')
  expect(out.classes()).toContain('tvo--fill')
  expect(out.text()).toContain('This terminal is open in its own window.')

  win.focus.mockClear()
  await out.get('[data-terminal-show-window]').trigger('click')
  expect(win.focus).toHaveBeenCalledTimes(1)
  await out.get('[data-terminal-bring-back]').trigger('click')
  await flushPromises()
  expect(win.close).toHaveBeenCalled()
  expect(w.find('[data-terminal-out]').exists()).toBe(false)
  expect(w.findAll('[data-fake-terminal]')).toHaveLength(2)
  w.unmount()
})

test('the terminal window itself (popout: false) never shows the placeholder', async () => {
  vi.spyOn(window, 'open').mockReturnValue({ closed: false, focus: vi.fn(), close: vi.fn() } as unknown as Window)
  usePopouts().open({ kind: 'user', id: 's1' }, 'user')
  const w = mount(defineComponent({ setup: () => () => h(Wrapped, { src: '/api/terminals/s1/socket', label: 'Terminal Home', popout: false }) }))
  await flushPromises()
  expect(w.find('[data-fake-terminal]').exists()).toBe(true)
  w.unmount()
})
