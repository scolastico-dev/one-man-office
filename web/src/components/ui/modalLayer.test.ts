import { expect, test } from 'vitest'
import { closeDialog, showModal, topModal } from './modalLayer'

test('modalLayer: the topmost open modal dialog is known until it closes', () => {
  const a = document.createElement('dialog')
  const b = document.createElement('dialog')
  document.body.append(a, b)
  showModal(a)
  showModal(b)
  expect(topModal.value).toBe(b)
  closeDialog(b)
  expect(topModal.value).toBe(a)
  closeDialog(a)
  expect(topModal.value).toBeNull()
  a.remove()
  b.remove()
})

// The toasts follow the topmost modal dialog only when every modal dialog is
// opened and closed through modalLayer.
const files = import.meta.glob(['../../**/*.{ts,vue}', '!../../**/*.test.ts', '!./modalLayer.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

test('modalLayer guard sees the app sources', () => {
  expect(Object.keys(files).length).toBeGreaterThan(100)
})

test.each(Object.entries(files))('modalLayer guard: no other code opens or closes a dialog itself: %s', (path, text) => {
  expect(/\.(showModal|close)\(\)/.test(text) && /HTMLDialogElement|<dialog/.test(text), path).toBe(false)
})
