import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { defineComponent, nextTick, ref } from 'vue'
import UiButton from './UiButton.vue'
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
