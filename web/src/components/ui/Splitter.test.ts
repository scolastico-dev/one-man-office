import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import Splitter from './Splitter.vue'

function setup(modelValue = 480) {
  const wrapper = mount(Splitter, {
    props: { modelValue, min: 320, max: 864, label: 'Sidebar width', 'onUpdate:modelValue': (v: number) => wrapper.setProps({ modelValue: v }) },
  })
  return { wrapper, sep: () => wrapper.get('[role="separator"]') }
}

describe('Splitter', () => {
  test('a focusable vertical separator with its name and values', () => {
    const { sep } = setup()
    expect(sep().attributes('aria-orientation')).toBe('vertical')
    expect(sep().attributes('aria-label')).toBe('Sidebar width')
    expect(sep().attributes('tabindex')).toBe('0')
    expect(sep().attributes('aria-valuenow')).toBe('480')
    expect(sep().attributes('aria-valuemin')).toBe('320')
    expect(sep().attributes('aria-valuemax')).toBe('864')
  })

  test('keys change the value within min and max; aria values follow', async () => {
    const { wrapper, sep } = setup()
    await sep().trigger('keydown', { key: 'ArrowRight' })
    expect(sep().attributes('aria-valuenow')).toBe('496')
    await sep().trigger('keydown', { key: 'ArrowLeft' })
    await sep().trigger('keydown', { key: 'ArrowLeft' })
    expect(sep().attributes('aria-valuenow')).toBe('464')
    await sep().trigger('keydown', { key: 'Home' })
    expect(sep().attributes('aria-valuenow')).toBe('320')
    // Already at the minimum: nothing changes, nothing is emitted.
    const count = wrapper.emitted('update:modelValue')!.length
    await sep().trigger('keydown', { key: 'ArrowLeft' })
    expect(wrapper.emitted('update:modelValue')!.length).toBe(count)
    await sep().trigger('keydown', { key: 'End' })
    expect(sep().attributes('aria-valuenow')).toBe('864')
    await sep().trigger('keydown', { key: 'ArrowRight' })
    expect(sep().attributes('aria-valuenow')).toBe('864')
    expect(wrapper.emitted('update:modelValue')).toEqual([[496], [480], [464], [320], [864]])
  })

  test('a value outside the range is shown clamped, and the next step starts from there', async () => {
    const { wrapper, sep } = setup(2000)
    expect(sep().attributes('aria-valuenow')).toBe('864')
    await sep().trigger('keydown', { key: 'ArrowLeft' })
    expect(wrapper.emitted('update:modelValue')).toEqual([[848]])
  })

  test('dragging with the pointer follows it within min and max', async () => {
    const { wrapper, sep } = setup()
    const el = sep().element as HTMLElement
    el.setPointerCapture = () => {}
    el.releasePointerCapture = () => {}
    await sep().trigger('pointerdown', { clientX: 100, pointerId: 1, button: 0 })
    await sep().trigger('pointermove', { clientX: 150, pointerId: 1 })
    expect(sep().attributes('aria-valuenow')).toBe('530')
    await sep().trigger('pointermove', { clientX: -900, pointerId: 1 })
    expect(sep().attributes('aria-valuenow')).toBe('320')
    await sep().trigger('pointerup', { clientX: -900, pointerId: 1 })
    await sep().trigger('pointermove', { clientX: 400, pointerId: 1 })
    expect(sep().attributes('aria-valuenow')).toBe('320')
    expect(wrapper.emitted('update:modelValue')).toEqual([[530], [320]])
  })
})
