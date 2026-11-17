import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import { nextTick } from 'vue'
import ReorderList from './ReorderList.vue'
import { announcements } from './useAnnouncer'

type Item = { id: string; label: string; note?: string }
const items: Item[] = [
  { id: 'claude/opus', label: 'opus', note: 'claude' },
  { id: 'claude/sonnet', label: 'sonnet' },
  { id: 'claude/sonnet', label: 'sonnet' },
]

function setup(list: Item[] = items) {
  const wrapper = mount(ReorderList, {
    attachTo: document.body,
    props: {
      items: list,
      label: 'Models of the developer',
      emptyText: 'No models yet.',
      // The owner applies the moves, as a page does.
      onMove: (from: number, to: number) => {
        const next = [...(wrapper.props('items') as Item[])]
        const [moved] = next.splice(from, 1)
        next.splice(to, 0, moved!)
        void wrapper.setProps({ items: next })
      },
      onRemove: (index: number) => {
        const next = [...(wrapper.props('items') as Item[])]
        next.splice(index, 1)
        void wrapper.setProps({ items: next })
      },
    },
  })
  const button = (name: string) => {
    const found = wrapper.findAll('button').find((b) => b.attributes('aria-label') === name)
    if (!found) throw new Error(`no button ${name}`)
    return found
  }
  const focused = () => (document.activeElement as HTMLElement | null)?.getAttribute('aria-label')
  return { wrapper, button, focused }
}

describe('ReorderList', () => {
  test('an ordered list named by its label, with places and notes', () => {
    const { wrapper, button } = setup()
    const list = wrapper.get('ol')
    expect(list.attributes('aria-label')).toBe('Models of the developer')
    const rows = wrapper.findAll('li')
    expect(rows.map((r) => r.get('.reorder__place').text())).toEqual(['1', '2', '3'])
    expect(rows[0]!.text()).toContain('claude')
    expect(button('Move opus up from place 1').attributes('disabled')).toBeDefined()
    expect(button('Move sonnet down from place 3').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  test('reorder list announces moves', async () => {
    const { wrapper, button, focused } = setup()
    await button('Move opus down from place 1').trigger('click')
    expect(wrapper.emitted('move')).toEqual([[0, 1]])
    await nextTick()
    expect(announcements.polite.value).toBe('opus moved to place 2.')
    await nextTick()
    expect(focused()).toBe('Move opus down from place 2')

    await button('Move sonnet up from place 3').trigger('click')
    expect(wrapper.emitted('move')!.at(-1)).toEqual([2, 1])
    await nextTick()
    expect(announcements.polite.value).toBe('sonnet moved to place 2.')
    wrapper.unmount()
  })

  test('remove emits the index, announces and keeps focus in the list', async () => {
    const { wrapper, button, focused } = setup()
    await button('Remove sonnet from place 2').trigger('click')
    expect(wrapper.emitted('remove')).toEqual([[1]])
    await nextTick()
    expect(announcements.polite.value).toBe('sonnet removed.')
    await nextTick()
    expect(focused()).toBe('Remove sonnet from place 2')
    wrapper.unmount()
  })

  test('an empty list says so', () => {
    const { wrapper } = setup([])
    expect(wrapper.find('ol').exists()).toBe(false)
    expect(wrapper.text()).toContain('No models yet.')
    wrapper.unmount()
  })
})
