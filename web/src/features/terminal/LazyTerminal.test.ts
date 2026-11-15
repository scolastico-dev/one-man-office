import { flushPromises, mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { defineComponent, h, provide, ref } from 'vue'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'
import { whileBoardShown } from './LazyTerminal'

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
