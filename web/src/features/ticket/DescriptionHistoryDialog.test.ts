import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ApiError } from '../../api/client'
import type { DescriptionVersion, TicketSummary } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import DescriptionHistoryDialog from './DescriptionHistoryDialog.vue'
import { mountPanel, ok, panelHarness, refuse } from './panelTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const v = (number: number, text: string, over: Partial<DescriptionVersion> = {}): DescriptionVersion => ({
  number,
  text,
  author: { role: 'user' },
  restored_from: null,
  created_at: '2026-11-01T10:00:00Z',
  current: false,
  ...over,
})

async function open(over: Partial<TicketSummary> = {}, props: { editing?: boolean } = {}) {
  const hn = await panelHarness((omo) => {
    omo.tickets = [ticket({ number: 4, description_version: 3, ...over })]
    omo.projects = [project('demo')]
  })
  const list = { value: [v(3, 'third <b>x</b>', { current: true, restored_from: 1, author: { role: 'pm', name: 'Mia' } }), v(1, 'first'), v(2, 'second')] }
  const orig = hn.session.client.get
  hn.session.client.get = ((path: string, q?: never) =>
    path === '/api/tickets/4/descriptions' ? Promise.resolve(ok(list.value)) : orig(path, q)) as typeof orig
  const restored: unknown[][] = []
  const closed: unknown[][] = []
  const { wrapper } = await mountPanel(
    hn,
    DescriptionHistoryDialog,
    {
      open: true,
      ticket: hn.session.store.view(4)!,
      editing: props.editing ?? false,
      onRestored: (...a: unknown[]) => restored.push(a),
      onClose: (...a: unknown[]) => closed.push(a),
    },
    '/4',
  )
  mounted = wrapper
  await flushPromises()
  return { hn, w: wrapper, list, restored, closed }
}

const numbers = (w: VueWrapper) => w.findAll('[data-version]').map((b) => b.attributes('data-version'))

test('history lists versions newest first, marks the current one and restored-from', async () => {
  const { w } = await open()
  expect(numbers(w)).toEqual(['3', '2', '1'])
  const first = w.get('[data-version="3"]')
  expect(first.get('[data-current-mark]').text()).toBe('current')
  expect(first.get('[data-restored-from]').text()).toBe('restored from v1')
  expect(first.text()).toContain('product manager Mia')
  expect(w.find('[data-version="2"] [data-current-mark]').exists()).toBe(false)
  // The current version is selected; its text is rendered as the description is, never as markup.
  expect(w.get('[data-history-preview]').text()).toContain('<b>x</b>')
  expect(w.find('[data-history-preview] b').exists()).toBe(false)
  // Selecting another shows its text.
  await w.get('[data-version="1"]').trigger('click')
  expect(w.get('[data-history-preview]').text()).toBe('first')
})

test('restore is disabled on the current version and on Done, and asks first while editing', async () => {
  const { hn, w, restored } = await open()
  const restore = () => w.get('[data-restore]')
  expect(restore().attributes('disabled')).toBeDefined()
  await w.get('[data-version="2"]').trigger('click')
  expect(restore().attributes('disabled')).toBeUndefined()
  hn.write.fn = () => ok(v(4, 'second', { restored_from: 2, current: true }), 9)
  await restore().trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/tickets/4/descriptions/2/restore', body: undefined }])
  expect(restored[0]).toEqual([expect.objectContaining({ number: 4 }), 9])
  w.unmount()
  mounted = null

  // Done: nothing to restore.
  const done = await open({ column: 'done', resolution: 'completed' })
  await done.w.get('[data-version="2"]').trigger('click')
  expect(done.w.get('[data-restore]').attributes('disabled')).toBeDefined()
  expect(done.w.get('[data-history-readonly]').text()).toContain('read-only')
  done.w.unmount()
  mounted = null

  // Editing: the first press asks; nothing is sent until it is confirmed.
  const ed = await open({}, { editing: true })
  await ed.w.get('[data-version="2"]').trigger('click')
  ed.hn.write.fn = () => ok(v(4, 'second', { restored_from: 2, current: true }), 9)
  await ed.w.get('[data-restore]').trigger('click')
  expect(ed.w.get('[data-restore-ask]').text()).toContain('Restoring replaces the text you are editing. Restore anyway?')
  expect(ed.hn.sent).toHaveLength(0)
  await ed.w.get('[data-restore-keep]').trigger('click')
  expect(ed.w.find('[data-restore-ask]').exists()).toBe(false)
  await ed.w.get('[data-restore]').trigger('click')
  await ed.w.get('[data-restore-anyway]').trigger('click')
  await flushPromises()
  expect(ed.hn.sent).toHaveLength(1)
})

test('a refusal and a lost answer show in the dialog', async () => {
  const { hn, w, restored } = await open()
  await w.get('[data-version="1"]').trigger('click')
  hn.write.fn = () => refuse('invalid_transition', '#4 is done; done tickets are read-only', 409)
  await w.get('[data-restore]').trigger('click')
  await flushPromises()
  expect(w.get('[data-history-error]').text()).toBe('#4 is done; done tickets are read-only')
  hn.write.fn = () => {
    throw new ApiError(0, 'network', 'offline')
  }
  await w.get('[data-restore]').trigger('click')
  await flushPromises()
  expect(w.get('[data-history-error]').text()).toContain('may have happened')
  expect(restored).toEqual([])
})

test('Done closes the dialog', async () => {
  const { w, closed } = await open()
  await w.get('[data-history-done]').trigger('click')
  expect(closed).toHaveLength(1)
})
