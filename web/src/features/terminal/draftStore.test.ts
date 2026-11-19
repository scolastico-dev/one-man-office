import { beforeEach, describe, expect, test } from 'vitest'
import { editDraft, insertImage, clearDraft, loadDraft, messageOf, saveDraft, MAX_MESSAGE_CHARS } from './draftStore'

beforeEach(() => window.localStorage.clear())

describe('draft storage', () => {
  test('a draft is kept per terminal and survives a new load', () => {
    saveDraft('a', insertImage({ text: 'hello', attachments: [] }, 5, 5, { path: '/x/one.png', name: 'one.png' }))
    saveDraft('b', { text: 'other', attachments: [] })
    expect(loadDraft('a')).toEqual({ text: 'hello[image 1]', attachments: [{ path: '/x/one.png', name: 'one.png', at: 5, marker: '[image 1]' }] })
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
    expect(loadDraft('b')).toEqual({ text: 'ok[image 1]', attachments: [{ path: '/q', name: 'q', at: 2, marker: '[image 1]' }] })
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
  test('text and image paths remain separate paste parts', () => {
    const d = insertImage({ text: 'look at this ', attachments: [] }, 13, 13, { path: '/a b/one.png', name: 'one.png' })
    expect(messageOf(d.text, d.attachments)).toEqual(['look at this ', "'/a b/one.png'"])
    expect(messageOf('only text', [])).toEqual(['only text'])
  })

  test('the text is cleaned like a paste: control characters go, tabs and line breaks stay', () => {
    expect(messageOf('a\x1b[201~b\x03c\td\r\ne', [])).toEqual(['a[201~bc\td\r\ne'])
  })

  test('blank drafts make no message', () => {
    expect(messageOf('  \n ', [])).toEqual([])
  })

  test('the limit leaves room for the markers inside the 64 KiB the server takes', () => {
    expect(MAX_MESSAGE_CHARS).toBeLessThan(64 * 1024 / 4)
  })
})

describe('inline image markers', () => {
  test('inserts at the selection and sends text and images in that order', () => {
    const d = insertImage({ text: 'before after', attachments: [] }, 7, 7, { path: '/a b.png', name: 'a.png' })
    expect(d.text).toBe('before [image 1]after')
    expect(messageOf(d.text, d.attachments)).toEqual(['before ', "'/a b.png'", 'after'])
    saveDraft('inline', d)
    expect(loadDraft('inline')).toEqual(d)
  })

  test('replacing any part of a marker removes the entire marker and its attachment', () => {
    const d = insertImage({ text: 'ab', attachments: [] }, 1, 1, { path: '/a.png', name: 'a.png' })
    expect(editDraft(d, 4, 5, 'x')).toEqual({ text: 'axb', attachments: [] })
    expect(editDraft(d, 9, 10, '')).toEqual({ text: 'ab', attachments: [] })
  })

  test('edits move the remaining markers and labels typed as text stay text', () => {
    let d = insertImage({ text: 'ab', attachments: [] }, 1, 1, { path: '/a.png', name: 'a.png' })
    d = editDraft(d, 0, 0, '[image 1]')
    expect(messageOf(d.text, d.attachments)).toEqual(['[image 1]a', '/a.png', 'b'])
    d = insertImage(d, d.text.length, d.text.length, { path: '/b.png', name: 'b.png' })
    expect(d.text).toBe('[image 1]a[image 1]b[image 2]')
    expect(messageOf(d.text, d.attachments)).toEqual(['[image 1]a', '/a.png', 'b', '/b.png'])
  })
})
