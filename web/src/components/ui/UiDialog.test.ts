import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { defineComponent, nextTick, provide, ref } from 'vue'
import UiButton from './UiButton.vue'
import { BOARD_SUSPENDED, DIALOGS_IN_PLACE } from './dialogHost'
import UiDialog from './UiDialog.vue'

const Host = defineComponent({
  components: { UiDialog, UiButton },
  setup() {
    const open = ref(false)
    return { open }
  },
  template: `
    <div>
      <button id="opener" @click="open = true">Create ticket</button>
      <UiDialog :open="open" title="Create ticket" eyebrow="board" @close="open = false">
        <p>Body</p>
        <template #actions>
          <UiButton @click="open = false">Cancel</UiButton>
          <UiButton variant="primary">Create</UiButton>
        </template>
      </UiDialog>
    </div>`,
})

test('UiDialog: Escape emits close and focus returns to the opener', async () => {
  const wrapper = mount(Host, { attachTo: document.body })
  const opener = wrapper.get('#opener')
  ;(opener.element as HTMLElement).focus()
  await opener.trigger('click')
  await nextTick()
  await nextTick()

  const dialog = wrapper.get('dialog')
  expect((dialog.element as HTMLDialogElement).open).toBe(true)
  expect(dialog.attributes('aria-labelledby')).toBe(wrapper.get('h2').attributes('id'))
  // No field in the body: the primary button gets focus.
  expect(document.activeElement?.textContent?.trim()).toBe('Create')

  await dialog.trigger('keydown', { key: 'Escape' })
  await nextTick()
  expect((dialog.element as HTMLDialogElement).open).toBe(false)
  expect(document.activeElement).toBe(opener.element)
  wrapper.unmount()
})

test('UiDialog: the first field gets focus when there is one', async () => {
  const wrapper = mount(UiDialog, {
    props: { open: false, title: 'Add project' },
    slots: { default: '<label>Path <input id="path" /></label>' },
    attachTo: document.body,
  })
  await wrapper.setProps({ open: true })
  await nextTick()
  expect(document.activeElement?.id).toBe('path')
  await wrapper.get('button[aria-label="Close"]').trigger('click')
  expect(wrapper.emitted('close')).toHaveLength(1)
  wrapper.unmount()
})

test('UiDialog: a dialog opened from inside a form sits in the body, so its events never reach the form', async () => {
  let hostSubmits = 0
  let hostClicks = 0
  const Nested = defineComponent({
    components: { UiDialog },
    setup: () => ({ onSubmit: () => hostSubmits++, onClick: () => hostClicks++ }),
    template: `
      <form id="host" @submit.prevent="onSubmit" @click="onClick">
        <UiDialog :open="true" title="Inner"><form id="inner" @submit.prevent><input name="x" /></form></UiDialog>
      </form>`,
  })
  const wrapper = mount(Nested, { attachTo: document.body, global: { provide: { [DIALOGS_IN_PLACE as symbol]: false } } })
  await nextTick()
  const inner = document.getElementById('inner')!
  expect(inner.closest('dialog')!.parentElement).toBe(document.body)
  expect(wrapper.get('#host').element.contains(inner)).toBe(false)
  inner.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
  inner.querySelector('input')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  expect(hostSubmits).toBe(0)
  expect(hostClicks).toBe(0)
  wrapper.unmount()
  expect(document.querySelector('dialog')).toBeNull()
})

test('UiDialog: steps aside while the board is suspended under the lock screen and comes back, without closing', async () => {
  const suspended = ref(false)
  const closed: string[] = []
  const Suspendable = defineComponent({
    components: { UiDialog },
    setup() {
      provide(BOARD_SUSPENDED, suspended)
      return { onClose: () => closed.push('close') }
    },
    template: `<UiDialog :open="true" title="Leave without saving?" @close="onClose"><p>Body</p></UiDialog>`,
  })
  const wrapper = mount(Suspendable, { attachTo: document.body, global: { provide: { [DIALOGS_IN_PLACE as symbol]: true } } })
  await nextTick()
  const el = wrapper.get('dialog').element as HTMLDialogElement
  expect(el.open).toBe(true)
  suspended.value = true
  await nextTick()
  await new Promise((r) => setTimeout(r, 0))
  expect(el.open).toBe(false)
  expect(closed).toEqual([])
  suspended.value = false
  await nextTick()
  expect(el.open).toBe(true)
  wrapper.unmount()
})

test('UiDialog: passes attributes such as data hooks on to the dialog element', async () => {
  const wrapper = mount(UiDialog, {
    attachTo: document.body,
    props: { open: true, title: 'Save anyway?' },
    attrs: { 'data-question': '' },
    global: { provide: { [DIALOGS_IN_PLACE as symbol]: true } },
  })
  await nextTick()
  expect(wrapper.find('dialog[data-question]').exists()).toBe(true)
  wrapper.unmount()
})

// A browser fires `close` as a task after `close()`; a key such as `c` can
// open the dialog again before that task runs. That late event belongs to the
// earlier close, so the reopened dialog stays open.
test('UiDialog: reopened before the close event of the earlier close arrives, it stays open', async () => {
  const wrapper = mount(Host, { attachTo: document.body })
  await wrapper.get('#opener').trigger('click')
  await nextTick()
  const dialog = wrapper.get('dialog')
  const el = dialog.element as HTMLDialogElement
  await dialog.trigger('keydown', { key: 'Escape' })
  await nextTick()
  expect(el.open).toBe(false)
  await wrapper.get('#opener').trigger('click')
  await nextTick()
  expect(el.open).toBe(true)
  el.dispatchEvent(new Event('close'))
  await nextTick()
  expect(el.open).toBe(true)
  wrapper.unmount()
})

test('UiDialog: back from the lock screen before the close event of stepping aside arrives, it stays open', async () => {
  const suspended = ref(false)
  const closed: string[] = []
  const Suspendable = defineComponent({
    components: { UiDialog },
    setup() {
      provide(BOARD_SUSPENDED, suspended)
      return { onClose: () => closed.push('close') }
    },
    template: `<UiDialog :open="true" title="Leave without saving?" @close="onClose"><p>Body</p></UiDialog>`,
  })
  const wrapper = mount(Suspendable, { attachTo: document.body, global: { provide: { [DIALOGS_IN_PLACE as symbol]: true } } })
  await nextTick()
  const el = wrapper.get('dialog').element as HTMLDialogElement
  suspended.value = true
  await nextTick()
  suspended.value = false
  await nextTick()
  expect(el.open).toBe(true)
  el.dispatchEvent(new Event('close'))
  await nextTick()
  expect(closed).toEqual([])
  wrapper.unmount()
})
