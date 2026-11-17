import { afterEach, describe, expect, test, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick, ref } from 'vue'
import UiButton from './UiButton.vue'

const live: Array<{ unmount(): void }> = []
afterEach(() => live.splice(0).forEach((w) => w.unmount()))

/** A button whose click starts a request: busy until `done()`. */
function busyButton() {
  const busy = ref(false)
  const clicks = vi.fn()
  const w = mount(
    defineComponent({
      setup: () => () => [
        h(UiButton, { disabled: busy.value, onClick: () => { clicks(); busy.value = true } }, () => 'Save'),
        h('input', { 'data-next': '' }),
      ],
    }),
    { attachTo: document.body },
  )
  live.push(w)
  return { w, busy, clicks, button: () => w.find('button').element as HTMLButtonElement }
}

describe('a disabled UiButton', () => {
  test('disabled from the start is natively disabled and takes no clicks', async () => {
    const { busy, clicks, button } = busyButton()
    busy.value = true
    await nextTick()
    expect(button().disabled).toBe(true)
    expect(button().hasAttribute('aria-disabled')).toBe(false)
    button().click()
    expect(clicks).not.toHaveBeenCalled()
  })

  test('disabled while it has the focus keeps the focus and ignores activation until the focus leaves', async () => {
    const { w, busy, clicks, button } = busyButton()
    button().focus()
    button().click()
    await nextTick()
    expect(clicks).toHaveBeenCalledTimes(1)
    expect(document.activeElement).toBe(button())
    expect(button().disabled).toBe(false)
    expect(button().getAttribute('aria-disabled')).toBe('true')
    // Enter or Space on a focused button is a click: it is ignored while busy.
    button().click()
    expect(clicks).toHaveBeenCalledTimes(1)
    // The focus moves on: now it is disabled like any other.
    ;(w.find('[data-next]').element as HTMLInputElement).focus()
    expect(button().disabled).toBe(true)
    expect(button().hasAttribute('aria-disabled')).toBe(false)
    busy.value = false
    await nextTick()
    expect(button().disabled).toBe(false)
    button().click()
    expect(clicks).toHaveBeenCalledTimes(2)
  })

  test('enabled again while it has the focus takes clicks again', async () => {
    const { busy, clicks, button } = busyButton()
    button().focus()
    button().click()
    await nextTick()
    busy.value = false
    await nextTick()
    expect(document.activeElement).toBe(button())
    expect(button().hasAttribute('aria-disabled')).toBe(false)
    button().click()
    expect(clicks).toHaveBeenCalledTimes(2)
  })

  test('a held submit button does not submit its form', async () => {
    const submits = vi.fn((e: Event) => e.preventDefault())
    const busy = ref(false)
    const w = mount(
      defineComponent({
        setup: () => () => h('form', { onSubmit: submits }, [h(UiButton, { type: 'submit', disabled: busy.value }, () => 'Save')]),
      }),
      { attachTo: document.body },
    )
    live.push(w)
    const b = w.find('button').element as HTMLButtonElement
    b.focus()
    busy.value = true
    await nextTick()
    b.click()
    expect(submits).not.toHaveBeenCalled()
  })
})
