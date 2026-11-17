import { expect, test } from 'vitest'
import { createTerminalViewers, placeOf } from './viewers'

test('the view claimed last is live; release gives it back to the one before; takeOver moves a view to live', () => {
  const v = createTerminalViewers()
  const a = v.claim('agent:1', 'the sidebar')
  expect(v.live('agent:1')?.id).toBe(a)
  const b = v.claim('agent:1', "the ticket's panel")
  expect(v.live('agent:1')).toEqual({ id: b, place: "the ticket's panel" })
  v.takeOver('agent:1', a)
  expect(v.live('agent:1')?.id).toBe(a)
  v.release('agent:1', a)
  expect(v.live('agent:1')?.id).toBe(b)
  v.release('agent:1', b)
  expect(v.live('agent:1')).toBeNull()
  expect(v.views.value.size).toBe(0)
  // Unknown ids change nothing.
  v.release('agent:1', 99)
  v.takeOver('agent:1', 99)
  expect(v.live('agent:1')).toBeNull()
})

test('views of different terminals do not affect each other; a place can be named later', () => {
  const v = createTerminalViewers()
  const a = v.claim('agent:1', 'another place in this window')
  const b = v.claim('user:2', 'the sidebar')
  v.setPlace('agent:1', a, 'the sidebar')
  expect(v.live('agent:1')).toEqual({ id: a, place: 'the sidebar' })
  expect(v.live('user:2')?.id).toBe(b)
})

test('placeOf names the sidebar, the panel and the page', () => {
  document.body.innerHTML = '<aside data-sidebar><i id="s"></i></aside><dialog data-ticket-panel><i id="p"></i></dialog><div data-ticket-page><i id="g"></i></div><i id="o"></i>'
  expect(placeOf(document.getElementById('s'))).toBe('the sidebar')
  expect(placeOf(document.getElementById('p'))).toBe("the ticket's panel")
  expect(placeOf(document.getElementById('g'))).toBe("the ticket's page")
  expect(placeOf(document.getElementById('o'))).toBe('another place in this window')
  document.body.innerHTML = ''
})
