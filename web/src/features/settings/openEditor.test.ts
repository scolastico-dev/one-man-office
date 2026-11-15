import { nextTick } from 'vue'
import { afterEach, expect, test, vi } from 'vitest'
import { busyNoteOf, useOpenEditor } from './openEditor'

afterEach(() => vi.unstubAllGlobals())

test('a busy editor does not go, and says why next to it', async () => {
  const e = useOpenEditor(() => 'the skill set docs')
  e.busy.value = true
  expect(await e.leave()).toBe(false)
  expect(e.busyNote.value).toBe(busyNoteOf('the skill set docs'))
  expect(e.busyNote.value).toBe('omo is still working on the skill set docs; it stays open until omo has answered.')
  // Once omo has answered the note goes, and the editor may go.
  e.busy.value = false
  await nextTick()
  expect(e.busyNote.value).toBe('')
  expect(await e.leave()).toBe(true)
})

test('unsaved edits ask first; keeping them keeps the editor', async () => {
  const confirm = vi.fn(() => false)
  vi.stubGlobal('confirm', confirm)
  const e = useOpenEditor(() => 'x')
  e.dirty.value = true
  expect(await e.leave()).toBe(false)
  expect(confirm).toHaveBeenCalledTimes(1)
  confirm.mockReturnValue(true)
  expect(await e.leave()).toBe(true)
  expect(e.dirty.value).toBe(false)
})

test('reset forgets everything of an editor that went away', () => {
  const e = useOpenEditor(() => 'x')
  e.dirty.value = true
  e.busy.value = true
  e.busyNote.value = 'n'
  e.reset()
  expect([e.dirty.value, e.busy.value, e.busyNote.value]).toEqual([false, false, ''])
})
