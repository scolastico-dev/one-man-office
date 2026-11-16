import { afterEach, expect, test } from 'vitest'
import { revealInBox, scrollBoxOf } from './scrollBox'

// happy-dom lays nothing out: sizes and places are given by hand.
function sized(el: HTMLElement, s: { top: number; height: number; scrollHeight?: number; clientHeight?: number }): void {
  el.getBoundingClientRect = () => ({ top: s.top, bottom: s.top + s.height, height: s.height, left: 0, right: 100, width: 100, x: 0, y: s.top, toJSON: () => ({}) }) as DOMRect
  Object.defineProperty(el, 'scrollHeight', { configurable: true, value: s.scrollHeight ?? s.height })
  Object.defineProperty(el, 'clientHeight', { configurable: true, value: s.clientHeight ?? s.height })
  Object.defineProperty(el, 'clientTop', { configurable: true, value: 0 })
}

function setup() {
  document.body.innerHTML = '<div id="outer" style="overflow-y: auto"><div id="hidden" style="overflow: hidden"><div id="box" style="overflow-y: auto"><p id="row">x</p></div></div></div>'
  const get = (id: string) => document.getElementById(id)!
  sized(get('outer'), { top: 0, height: 1000, scrollHeight: 3000 })
  sized(get('hidden'), { top: 0, height: 900, scrollHeight: 2000 })
  sized(get('box'), { top: 100, height: 200, scrollHeight: 5000 })
  return { outer: get('outer'), hidden: get('hidden'), box: get('box'), row: get('row') }
}

afterEach(() => {
  document.body.innerHTML = ''
})

test('the box of an element is its nearest ancestor that scrolls and overflows; a box with hidden overflow is passed over', () => {
  const { box, row, outer, hidden } = setup()
  expect(scrollBoxOf(row)).toBe(box)
  expect(scrollBoxOf(box)).toBe(outer)
  expect(scrollBoxOf(hidden)).toBe(outer)
  // A box with nothing more to show does not count.
  Object.defineProperty(box, 'scrollHeight', { configurable: true, value: 200 })
  expect(scrollBoxOf(row)).toBe(outer)
  expect(scrollBoxOf(outer)).toBeNull()
})

test('nearest moves the box by as little as shows the row, and not at all when it shows', () => {
  const { box, row, outer, hidden } = setup()
  sized(row, { top: 350, height: 20 })
  revealInBox(row)
  expect(box.scrollTop).toBe(70)
  box.scrollTop = 100
  sized(row, { top: 50, height: 20 })
  revealInBox(row)
  expect(box.scrollTop).toBe(50)
  sized(row, { top: 150, height: 20 })
  revealInBox(row)
  expect(box.scrollTop).toBe(50)
  // Only the nearest box moved.
  expect(outer.scrollTop).toBe(0)
  expect(hidden.scrollTop).toBe(0)
})

test('start puts the row at the top of the box, center in its middle', () => {
  const { box, row } = setup()
  sized(row, { top: 500, height: 20 })
  revealInBox(row, 'start')
  expect(box.scrollTop).toBe(400)
  box.scrollTop = 0
  revealInBox(row, 'center')
  expect(box.scrollTop).toBe(400 - 90)
})

test('without a box that scrolls, nothing moves', () => {
  document.body.innerHTML = '<p id="row">x</p>'
  const row = document.getElementById('row')!
  sized(row, { top: 5000, height: 20 })
  revealInBox(row)
  expect(document.documentElement.scrollTop).toBe(0)
})
