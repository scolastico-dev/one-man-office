import { mount } from '@vue/test-utils'
import { expect, it } from 'vitest'
import App from './App.vue'

it('mounts and shows the name', () => {
  const wrapper = mount(App)
  expect(wrapper.text()).toContain('omo')
})
