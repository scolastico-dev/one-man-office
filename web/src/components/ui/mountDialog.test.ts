import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { defineComponent, h, inject, provide, ref } from 'vue'
import { BOARD_SUSPENDED } from './dialogHost'
import { createDialogScope, DialogOutlet, mountDialog, useDialogScope, type DialogScope } from './mountDialog'
import UiDialog from './UiDialog.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  document.body.innerHTML = ''
})

const FROM_OWNER = Symbol('from-owner')

/** An owner that provides a value and the lock flag, and keeps its dialogs in a scope of its own. */
function owner(suspended = ref(false)) {
  let scope!: DialogScope
  const Owner = defineComponent({
    setup() {
      provide(FROM_OWNER, 'the owner')
      provide(BOARD_SUSPENDED, suspended)
      scope = useDialogScope()
      return () => h('div', [h(DialogOutlet, { scope })])
    },
  })
  mounted = mount(Owner, { attachTo: document.body })
  return { scope, suspended, wrapper: mounted }
}

const Probe = defineComponent({
  setup() {
    const seen = inject(FROM_OWNER, 'nothing')
    return () => h('p', { 'data-probe': '' }, seen)
  },
})

test('a dialog is rendered in the owner, with what the owner provides', async () => {
  const { scope } = owner()
  mountDialog(() => h(Probe), scope)
  await flushPromises()
  expect(document.querySelector('[data-probe]')?.textContent).toBe('the owner')
  expect(scope.open).toHaveLength(1)
})

test('closing calls onClosed once, from the dialog or from the opener', async () => {
  const { scope } = owner()
  const closed = vi.fn()
  let inner!: () => void
  const close = mountDialog((c) => {
    inner = c
    return h(Probe)
  }, scope, closed)
  await flushPromises()
  inner()
  close()
  await flushPromises()
  expect(closed).toHaveBeenCalledTimes(1)
  expect(document.querySelector('[data-probe]')).toBeNull()
})

test('the owner going away closes its dialogs and tells their openers', async () => {
  const { scope, wrapper } = owner()
  const closed = vi.fn()
  mountDialog(() => h(Probe), scope, closed)
  mountDialog(() => h(Probe), scope, closed)
  await flushPromises()
  wrapper.unmount()
  mounted = null
  expect(closed).toHaveBeenCalledTimes(2)
  expect(scope.open).toHaveLength(0)
  expect(document.querySelector('[data-probe]')).toBeNull()
})

test('closeAll closes every dialog of the scope', async () => {
  const scope = createDialogScope()
  const closed = vi.fn()
  mountDialog(() => null, scope, closed)
  mountDialog(() => null, scope, closed)
  scope.closeAll()
  expect(closed).toHaveBeenCalledTimes(2)
})

test('under the lock a dialog from code steps aside like every other dialog, and comes back after', async () => {
  const { scope, suspended } = owner()
  mountDialog((close) => h(UiDialog, { open: true, title: 'Asks', onClose: close, 'data-asks': '' }, { default: () => 'text' }), scope)
  await flushPromises()
  const dialog = document.querySelector<HTMLDialogElement>('dialog[data-asks]')!
  expect(dialog.open).toBe(true)
  suspended.value = true
  await flushPromises()
  expect(dialog.open).toBe(false)
  suspended.value = false
  await flushPromises()
  expect(dialog.open).toBe(true)
})
