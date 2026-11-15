import { mount } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { nextTick } from 'vue'
import Toaster from './Toaster.vue'
import { activeToasts, useToast } from './useToast'

afterEach(() => {
  const { dismiss } = useToast()
  for (const t of [...activeToasts]) dismiss(t.id)
  vi.useRealTimers()
})

test('Toaster: error toasts are in a role=alert region and dismissable', async () => {
  const wrapper = mount(Toaster)
  const { show } = useToast()
  show({ title: 'Move refused', message: 'Project folder is missing.', tone: 'error' })
  show({ title: 'Saved' })
  await nextTick()

  const alert = wrapper.get('[role="alert"]')
  const status = wrapper.get('[role="status"]')
  expect(alert.text()).toContain('Move refused')
  expect(alert.text()).toContain('Project folder is missing.')
  expect(alert.text()).not.toContain('Saved')
  expect(status.text()).toContain('Saved')

  await alert.get('button[aria-label="Dismiss"]').trigger('click')
  expect(wrapper.get('[role="alert"]').text()).toBe('')
  expect(wrapper.get('[role="status"]').text()).toContain('Saved')
})

test('Toaster: errors stay 8 seconds, the action runs and closes the toast', async () => {
  vi.useFakeTimers()
  const wrapper = mount(Toaster)
  const { show } = useToast()
  const run = vi.fn()
  show({ title: 'Move refused', tone: 'error' })
  show({ title: 'Ticket created', action: { label: 'Open', run }, timeoutMs: 0 })
  await nextTick()

  vi.advanceTimersByTime(7900)
  await nextTick()
  expect(wrapper.get('[role="alert"]').text()).toContain('Move refused')
  vi.advanceTimersByTime(200)
  await nextTick()
  expect(wrapper.get('[role="alert"]').text()).toBe('')

  await wrapper.get('[role="status"] button:not([aria-label])').trigger('click')
  expect(run).toHaveBeenCalledOnce()
  expect(activeToasts).toHaveLength(0)
})
