import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import { nextTick } from 'vue'
import EnvTable from './EnvTable.vue'

function setup(props: Record<string, unknown> = {}) {
  const wrapper = mount(EnvTable, {
    attachTo: document.body,
    props: {
      label: 'Agent environment',
      modelValue: { PATH: '$HOME/bin:$PATH', ANTHROPIC_API_KEY: 'sk-ant-1', GIT_AUTHOR_NAME: 'omo' } as Record<string, string>,
      'onUpdate:modelValue': (v: Record<string, string>) => wrapper.setProps({ modelValue: v }),
      ...props,
    },
  })
  const model = () => wrapper.props('modelValue') as Record<string, string>
  const named = (name: string) => {
    const found = wrapper.findAll('button, input').find((el) => el.attributes('aria-label') === name || el.text() === name)
    if (!found) throw new Error(`nothing named ${name}`)
    return found
  }
  const names = () => wrapper.findAll('[data-env-row]').map((r) => (r.get('input').element as HTMLInputElement).value)
  return { wrapper, model, named, names }
}

describe('EnvTable', () => {
  test('a group named by its label; rows sorted by name on load', () => {
    const { wrapper, names, named } = setup()
    const group = wrapper.get('[role="group"]')
    expect(document.getElementById(group.attributes('aria-labelledby')!)!.textContent).toBe('Agent environment')
    expect(names()).toEqual(['ANTHROPIC_API_KEY', 'GIT_AUTHOR_NAME', 'PATH'])
    expect((named('Value of PATH').element as HTMLInputElement).value).toBe('$HOME/bin:$PATH')
    expect(named('Name of variable 3').exists()).toBe(true)
    wrapper.unmount()
  })

  test('every value is an ordinary text input; shipped defaults marked', async () => {
    const { wrapper, model, named } = setup({ shipped: { GIT_AUTHOR_NAME: 'omo', PATH: '$PATH' } })
    const key = named('Value of ANTHROPIC_API_KEY')
    expect((key.element as HTMLInputElement).value).toBe('sk-ant-1')
    expect(key.attributes('type') ?? 'text').toBe('text')
    expect(wrapper.findAll('[data-env-row] input')).toHaveLength(6)

    // Shipped: marked only while name and value are the shipped ones.
    expect(wrapper.get('[data-env-row="GIT_AUTHOR_NAME"]').text()).toContain('shipped default')
    expect(wrapper.get('[data-env-row="PATH"]').text()).not.toContain('shipped default')

    await key.setValue('`pass show anthropic`')
    expect(model().ANTHROPIC_API_KEY).toBe('`pass show anthropic`')
    wrapper.unmount()
  })

  test('remove, add and rename keep the row order while typing', async () => {
    const { wrapper, model, named, names } = setup()
    await named('Remove ANTHROPIC_API_KEY').trigger('click')
    expect(Object.keys(model())).toEqual(['GIT_AUTHOR_NAME', 'PATH'])
    await nextTick()
    expect(document.activeElement).toBe(named('Name of variable 1').element)

    await named('Add variable').trigger('click')
    await nextTick()
    const name = named('Name of variable 3')
    expect(document.activeElement).toBe(name.element)
    // A row without a name is not part of the value yet.
    expect(Object.keys(model())).toEqual(['GIT_AUTHOR_NAME', 'PATH'])
    await name.setValue('AAA')
    await named('Value of AAA').setValue('1')
    expect(model()).toEqual({ GIT_AUTHOR_NAME: 'omo', PATH: '$HOME/bin:$PATH', AAA: '1' })
    // Not sorted while typing.
    expect(names()).toEqual(['GIT_AUTHOR_NAME', 'PATH', 'AAA'])

    // A new value from outside is sorted again.
    await wrapper.setProps({ modelValue: { Z: '1', B: '2' } })
    expect(names()).toEqual(['B', 'Z'])
    wrapper.unmount()
  })

  test('a name used twice is said; errors by name are linked to the row', async () => {
    const { wrapper, named } = setup({ errors: { PATH: 'agent_env.PATH: unterminated ${' } })
    const value = named('Value of PATH')
    expect(value.attributes('aria-invalid')).toBe('true')
    expect(document.getElementById(value.attributes('aria-describedby')!)!.textContent).toBe('agent_env.PATH: unterminated ${')

    await named('Name of variable 2').setValue('PATH')
    expect(wrapper.text()).toContain('PATH is set twice; only the last row counts.')
    wrapper.unmount()
  })

  test('an empty table says so', () => {
    const { wrapper } = setup({ modelValue: {}, emptyText: 'No variables.' })
    expect(wrapper.text()).toContain('No variables.')
    wrapper.unmount()
  })
})
