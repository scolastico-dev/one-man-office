import { flushPromises, mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { DIALOGS_IN_PLACE } from '../../components/ui/dialogHost'
import NameDialog from './NameDialog.vue'

const words = { title: 'New script', eyebrow: 'usage', thing: 'script', confirm: 'Create' }

function open(taken: string[] = []) {
  const created: string[] = []
  let closed = 0
  const w = mount(NameDialog, {
    props: { open: true, taken, ...words, onCreate: (n: string) => created.push(n), onClose: () => closed++ },
    global: { provide: { [DIALOGS_IN_PLACE as symbol]: true } },
  })
  return { w, created, closed: () => closed }
}

test('the page gives the words; an empty name and one in use are refused in place', async () => {
  const { w, created } = open(['claude'])
  expect(w.get('dialog').text()).toContain('New script')
  expect(w.get('label').text()).toContain('Name of the script')
  expect(w.get('[data-primary]').text()).toBe('Create')
  await w.get('form').trigger('submit')
  expect(w.text()).toContain('Enter a name.')
  await w.get('input').setValue(' claude ')
  await w.get('form').trigger('submit')
  expect(w.text()).toContain('A script claude exists already.')
  expect(created).toEqual([])
  w.unmount()
})

test('a free name is handed to the page, trimmed, and the dialog closes', async () => {
  const { w, created, closed } = open()
  await w.get('input').setValue(' mine ')
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(created).toEqual(['mine'])
  expect(closed()).toBe(1)
  w.unmount()
})
