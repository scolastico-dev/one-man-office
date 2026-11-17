import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import Callout from './Callout.vue'

describe('Callout', () => {
  test.each([
    ['info', 'Note'],
    ['warn', 'Warning'],
    ['danger', 'Problem'],
  ] as const)('callout says its tone in words (%s)', (tone, word) => {
    const wrapper = mount(Callout, { props: { tone }, slots: { default: 'settings.yaml cannot be read.' } })
    expect(wrapper.get('[data-tone-word]').text()).toBe(word)
    expect(wrapper.text()).toContain('settings.yaml cannot be read.')
    expect(wrapper.attributes('data-tone')).toBe(tone)
  })

  test('a title follows the tone word', () => {
    const wrapper = mount(Callout, { props: { tone: 'danger', title: 'profiles/claude.yaml' }, slots: { default: 'line 3: bad' } })
    expect(wrapper.get('[data-tone-word]').text()).toBe('Problem')
    expect(wrapper.get('.callout__title').text()).toBe('profiles/claude.yaml')
  })

  test('not live by default; polite is a status, assertive an alert', async () => {
    const wrapper = mount(Callout, { props: { tone: 'info' }, slots: { default: 'x' } })
    expect(wrapper.attributes('role')).toBe('note')
    await wrapper.setProps({ live: 'polite' })
    expect(wrapper.attributes('role')).toBe('status')
    await wrapper.setProps({ live: 'assertive' })
    expect(wrapper.attributes('role')).toBe('alert')
  })

  test('actions render in their own row', () => {
    const wrapper = mount(Callout, { props: { tone: 'warn' }, slots: { default: 'x', actions: '<button>Use the new value</button>' } })
    expect(wrapper.get('.callout__actions button').text()).toBe('Use the new value')
  })
})
