import { beforeEach, describe, expect, test } from 'vitest'
import { clearDraft, loadDraft, messageOf, pasteOf, saveDraft, MAX_MESSAGE_CHARS } from './draftStore'

beforeEach(() => window.localStorage.clear())

describe('draft storage', () => {
  test('a draft is kept per terminal and survives a new load', () => {
    saveDraft('a', { text: 'hello', attachments: [{ path: '/x/one.png', name: 'one.png' }] })
    saveDraft('b', { text: 'other', attachments: [] })
    expect(loadDraft('a')).toEqual({ text: 'hello', attachments: [{ path: '/x/one.png', name: 'one.png' }] })
    expect(loadDraft('b').text).toBe('other')
    expect(loadDraft('c')).toEqual({ text: '', attachments: [] })
  })

  test('an empty draft is removed, and clearing removes it', () => {
    saveDraft('a', { text: 'x', attachments: [] })
    saveDraft('a', { text: '', attachments: [] })
    expect(window.localStorage.getItem('omo.terminalDrafts')).toBeNull()
    saveDraft('a', { text: 'x', attachments: [] })
    clearDraft('a')
    expect(loadDraft('a').text).toBe('')
  })

  test('broken or foreign stored data gives an empty draft', () => {
    window.localStorage.setItem('omo.terminalDrafts', '{not json')
    expect(loadDraft('a')).toEqual({ text: '', attachments: [] })
    window.localStorage.setItem('omo.terminalDrafts', JSON.stringify({ a: { text: 5, attachments: [{ path: 1 }] }, b: { text: 'ok', attachments: [{ path: '/p', name: 3 }, { path: '/q', name: 'q' }] } }))
    expect(loadDraft('a')).toEqual({ text: '', attachments: [] })
    expect(loadDraft('b')).toEqual({ text: 'ok', attachments: [{ path: '/q', name: 'q' }] })
  })

  test('drafts older than two weeks are dropped when another is saved', () => {
    const old = Date.now() - 15 * 86_400_000
    window.localStorage.setItem('omo.terminalDrafts', JSON.stringify({ gone: { text: 'old', attachments: [], at: old } }))
    saveDraft('a', { text: 'new', attachments: [] })
    expect(loadDraft('gone').text).toBe('')
    expect(loadDraft('a').text).toBe('new')
  })

  test('blocked storage does not throw', () => {
    const spy = Object.getOwnPropertyDescriptor(window, 'localStorage')!
    Object.defineProperty(window, 'localStorage', { get: () => { throw new Error('blocked') }, configurable: true })
    try {
      expect(() => saveDraft('a', { text: 'x', attachments: [] })).not.toThrow()
      expect(loadDraft('a').text).toBe('')
    } finally {
      Object.defineProperty(window, 'localStorage', spy)
    }
  })
})

describe('the message', () => {
  test('text and the image paths form one message, the paths on their own line', () => {
    expect(messageOf('look at this', ['/a b/one.png', '/two.png'])).toBe("look at this\n'/a b/one.png' /two.png")
    expect(messageOf('', ['/two.png'])).toBe('/two.png')
    expect(messageOf('only text', [])).toBe('only text')
  })

  test('the text is cleaned like a paste: control characters go, tabs and line breaks stay', () => {
    expect(messageOf('a\x1b[201~b\x03c\td\r\ne', [])).toBe('a[201~bc\td\r\ne')
  })

  test('blank drafts make no message', () => {
    expect(messageOf('  \n ', [])).toBe('')
  })

  test('in bracketed paste mode the message is wrapped as one paste, line breaks as the emulator sends them', () => {
    expect(pasteOf('one\ntwo\r\nthree', true)).toBe('\x1b[200~one\rtwo\rthree\x1b[201~')
  })

  test('without bracketed paste only the line breaks are converted', () => {
    expect(pasteOf('one\ntwo', false)).toBe('one\rtwo')
  })

  test('the limit leaves room for the markers inside the 64 KiB the server takes', () => {
    expect(MAX_MESSAGE_CHARS).toBeLessThan(64 * 1024 / 4)
  })
})
