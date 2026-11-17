import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import LockedScreen from './LockedScreen.vue'

const TOKEN = 'c0ffee'.repeat(10) + 'c0ff'

test('explains how to unlock, with the field labelled and focused', () => {
  const wrapper = mount(LockedScreen, { props: { reason: 'missing' }, attachTo: document.body })
  expect(wrapper.get('h1').text()).toBe('omo is locked')
  expect(wrapper.text()).toContain('Open the address omo printed when it started. It ends with # and the login token.')
  expect(wrapper.text()).toContain('Lost it? Run omo in a terminal: a running omo prints its address again.')
  expect(wrapper.find('[data-rejected]').exists()).toBe(false)
  const input = wrapper.get('input')
  expect(wrapper.get(`label[for="${input.attributes('id')}"]`).text()).toBe('Access address or token')
  expect(document.activeElement).toBe(input.element)
  wrapper.unmount()
})

test('a rejected token adds the rotate hint', () => {
  const wrapper = mount(LockedScreen, { props: { reason: 'rejected' } })
  expect(wrapper.get('[data-rejected]').text()).toBe(
    'The saved login token was not accepted. It changes when someone runs omo config rotate-token.',
  )
})

test('accepts the printed address or the bare token and emits the token', async () => {
  const wrapper = mount(LockedScreen, { props: { reason: 'missing' } })
  await wrapper.get('input').setValue(`  http://omo.localhost:4100/#${TOKEN} `)
  await wrapper.get('form').trigger('submit')
  await wrapper.get('input').setValue(TOKEN)
  await wrapper.get('form').trigger('submit')
  expect(wrapper.emitted('unlocked')).toEqual([[TOKEN], [TOKEN]])
  // The token does not stay on screen.
  expect((wrapper.get('input').element as HTMLInputElement).value).toBe('')
})

test('anything else shows the error under the field and emits nothing', async () => {
  const wrapper = mount(LockedScreen, { props: { reason: 'missing' } })
  await wrapper.get('input').setValue('http://omo.localhost:4100/')
  await wrapper.get('form').trigger('submit')
  expect(wrapper.emitted('unlocked')).toBeUndefined()
  const input = wrapper.get('input')
  expect(input.attributes('aria-invalid')).toBe('true')
  expect(wrapper.get(`#${input.attributes('aria-describedby')}`).text()).toBe('That is not an omo access address or token.')
})
