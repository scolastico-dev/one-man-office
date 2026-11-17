import { afterEach, expect, test, vi } from 'vitest'
import { modalOpen, pageKey, plainKey, typing } from './shortcuts'

afterEach(() => {
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

const key = (key: string, target: EventTarget, o: KeyboardEventInit = {}) => {
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...o })
  Object.defineProperty(e, 'target', { value: target })
  return e
}

/** A dialog that is open; jsdom knows no top layer, so whether it is modal is said here. */
function dialog(modal: boolean): HTMLDialogElement {
  const d = document.createElement('dialog')
  d.setAttribute('open', '')
  const matches = d.matches.bind(d)
  d.matches = ((sel: string) => (sel === ':modal' ? modal : matches(sel))) as typeof d.matches
  document.body.append(d)
  return d
}

test('fields, editable text and terminals take their keys', () => {
  const input = document.createElement('input')
  const term = document.createElement('div')
  term.className = 'xterm'
  const inner = document.createElement('textarea')
  term.append(inner)
  const span = document.createElement('div')
  term.append(span)
  expect(typing(input)).toBe(true)
  expect(typing(inner)).toBe(true)
  expect(typing(span)).toBe(true)
  expect(typing(document.createElement('button'))).toBe(false)
  expect(typing(null)).toBe(false)
})

test('a plain key is the key without Ctrl, Alt or Meta, outside a field', () => {
  const b = document.createElement('button')
  expect(plainKey(key('c', b), 'c')).toBe(true)
  expect(plainKey(key('/', b, { shiftKey: true }), '/')).toBe(true)
  expect(plainKey(key('c', b, { ctrlKey: true }), 'c')).toBe(false)
  expect(plainKey(key('c', document.createElement('input')), 'c')).toBe(false)
})

test('only a modal dialog stops the page keys; the ticket panel beside the board does not', () => {
  const b = document.createElement('button')
  const panel = dialog(false)
  expect(modalOpen()).toBe(false)
  expect(pageKey(key('c', b), 'c')).toBe(true)
  const create = dialog(true)
  expect(modalOpen()).toBe(true)
  expect(modalOpen(create)).toBe(false)
  expect(pageKey(key('c', b), 'c')).toBe(false)
  panel.remove()
})
