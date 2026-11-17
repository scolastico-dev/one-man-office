import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import { nextTick } from 'vue'
import ListField from './ListField.vue'
import { announcements } from './useAnnouncer'

function setup(props: Record<string, unknown> = {}) {
  const wrapper = mount(ListField, {
    attachTo: document.body,
    props: {
      label: 'Base arguments',
      itemLabel: 'Argument',
      addLabel: 'Add argument',
      emptyText: 'No arguments.',
      modelValue: ['--verbose', '--model', 'opus'] as string[],
      'onUpdate:modelValue': (v: string[]) => wrapper.setProps({ modelValue: v }),
      ...props,
    },
  })
  const model = () => wrapper.props('modelValue') as string[]
  const byName = (name: string) => {
    const found = wrapper.findAll('button, input').find((el) => el.attributes('aria-label') === name || el.text() === name)
    if (!found) throw new Error(`nothing named ${name}`)
    return found
  }
  const focusedName = () => {
    const el = document.activeElement as HTMLElement | null
    return el?.getAttribute('aria-label') ?? el?.textContent?.trim()
  }
  return { wrapper, model, byName, focusedName }
}

describe('ListField', () => {
  test('a group named by its label; rows named by their position', () => {
    const { wrapper, byName } = setup()
    const group = wrapper.get('[role="group"]')
    expect(document.getElementById(group.attributes('aria-labelledby')!)!.textContent).toBe('Base arguments')
    expect((byName('Argument 2').element as HTMLInputElement).value).toBe('--model')
    expect(byName('Move argument 1 up').attributes('disabled')).toBeDefined()
    expect(byName('Move argument 3 down').attributes('disabled')).toBeDefined()
    expect(byName('Move argument 2 up').attributes('disabled')).toBeUndefined()
    expect(byName('Remove argument 3').exists()).toBe(true)
    wrapper.unmount()
  })

  test('list field adds, removes, moves with buttons and names them by position', async () => {
    const { wrapper, model, byName, focusedName } = setup()
    await byName('Move argument 3 up').trigger('click')
    expect(model()).toEqual(['--verbose', 'opus', '--model'])
    // Focus follows the row to its new place.
    await nextTick()
    expect(focusedName()).toBe('Move argument 2 up')
    await nextTick()
    expect(announcements.polite.value).toBe('Argument moved to place 2.')

    await byName('Move argument 1 down').trigger('click')
    expect(model()).toEqual(['opus', '--verbose', '--model'])

    await byName('Move argument 2 up').trigger('click')
    expect(model()).toEqual(['--verbose', 'opus', '--model'])
    await nextTick()
    // At the top, Move up is disabled: focus goes to Move down of the same row.
    expect(focusedName()).toBe('Move argument 1 down')

    await byName('Remove argument 2').trigger('click')
    expect(model()).toEqual(['--verbose', '--model'])
    await nextTick()
    expect(focusedName()).toBe('Argument 2')

    await byName('Add argument').trigger('click')
    expect(model()).toEqual(['--verbose', '--model', ''])
    await nextTick()
    expect(focusedName()).toBe('Argument 3')
    await byName('Argument 3').setValue('--fast')
    expect(model()).toEqual(['--verbose', '--model', '--fast'])
    wrapper.unmount()
  })

  test('removing the last row shows the empty text and focuses Add', async () => {
    const { wrapper, model, byName, focusedName } = setup({ modelValue: ['x'] })
    await byName('Remove argument 1').trigger('click')
    expect(model()).toEqual([])
    expect(wrapper.text()).toContain('No arguments.')
    await nextTick()
    expect(focusedName()).toBe('Add argument')
    wrapper.unmount()
  })

  test('errors per row are linked to their input', () => {
    const { wrapper, byName } = setup({ errors: { 1: 'arguments.1 must not be empty' } })
    const input = byName('Argument 2')
    expect(input.attributes('aria-invalid')).toBe('true')
    const error = document.getElementById(input.attributes('aria-describedby')!)!
    expect(error.textContent).toBe('arguments.1 must not be empty')
    expect(byName('Argument 1').attributes('aria-invalid')).toBeUndefined()
    wrapper.unmount()
  })

  test('a hint and an error for the whole list describe the group', () => {
    const { wrapper } = setup({ hint: 'Passed before the prompt.', error: 'at most 100 arguments' })
    const group = wrapper.get('[role="group"]')
    const ids = group.attributes('aria-describedby')!.split(' ')
    expect(ids.map((id) => document.getElementById(id)!.textContent)).toEqual(['Passed before the prompt.', 'at most 100 arguments'])
    wrapper.unmount()
  })
})
