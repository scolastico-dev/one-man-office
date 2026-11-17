import { describe, expect, test } from 'vitest'
import { SseParser, type SseFrame } from './sse'

const enc = new TextEncoder()

/** Decodes byte chunks the way TextDecoderStream does and feeds the parser. */
function parseChunks(chunks: Uint8Array[]): { frames: SseFrame[]; retry: number | null; comments: number } {
  const p = new SseParser()
  const dec = new TextDecoder()
  const frames: SseFrame[] = []
  for (const c of chunks) frames.push(...p.push(dec.decode(c, { stream: true })))
  frames.push(...p.push(dec.decode()))
  return { frames, retry: p.retry, comments: p.comments }
}

// A stream as the server writes it: the retry preamble, a resync frame (no
// id), events whose data holds multi-byte characters, and keep-alive pings.
const recorded =
  'retry: 3000\n\n' +
  'event: resync\ndata: {"latest":12}\n\n' +
  'id: 13\nevent: ticket.updated\ndata: {"id":13,"kind":"ticket.updated","data":{"title":"Grüße – 日本 🎉"}}\n\n' +
  ': ping\n\n' +
  'id: 14\nevent: ticket.moved\ndata: {"id":14,"kind":"ticket.moved","data":{"from":"review","to":"review"}}\n\n' +
  ': ping\n\n'

const expected: SseFrame[] = [
  { id: null, event: 'resync', data: '{"latest":12}' },
  {
    id: '13',
    event: 'ticket.updated',
    data: '{"id":13,"kind":"ticket.updated","data":{"title":"Grüße – 日本 🎉"}}',
  },
  { id: '14', event: 'ticket.moved', data: '{"id":14,"kind":"ticket.moved","data":{"from":"review","to":"review"}}' },
]

describe('SseParser', () => {
  test('parser: id applies only when the frame is complete, split across chunks', () => {
    const p = new SseParser()
    expect(p.push('id: 5\nevent: ticket.updated\nda')).toEqual([])
    expect(p.push('ta: {"a":1}\n')).toEqual([])
    // The frame is not complete until its blank line arrived.
    expect(p.push('')).toEqual([])
    expect(p.push('\n')).toEqual([{ id: '5', event: 'ticket.updated', data: '{"a":1}' }])
    // An id is not carried over into a following frame without one.
    expect(p.push('data: x\n\n')).toEqual([{ id: null, event: 'message', data: 'x' }])
    // A frame cut off by the end of the stream never counts.
    expect(p.push('id: 6\ndata: y\n')).toEqual([])
  })

  test('parser: CRLF, CR, BOM, comments and multi-line data', () => {
    const p = new SseParser()
    const frames = p.push(
      '﻿: hello\r\nid: 1\r\ndata: a\r\ndata:b\r\ndata\r\n\r\n' +
        'id: 2\rdata: c\r\r' +
        ':only a comment\n\n' +
        'id: 3\nevent: x\nunknown: field\ndata:  two spaces\n\n',
    )
    expect(frames).toEqual([
      { id: '1', event: 'message', data: 'a\nb\n' },
      { id: '2', event: 'message', data: 'c' },
      { id: '3', event: 'x', data: ' two spaces' },
    ])
    expect(p.comments).toBe(2)
  })

  test('a BOM is stripped only at the very start of the stream', () => {
    const p = new SseParser()
    expect(p.push('')).toEqual([])
    expect(p.push('﻿data: a\n\n﻿data: b\n\n')).toEqual([{ id: null, event: 'message', data: 'a' }])
  })

  test('retry takes digits only and frames without data dispatch nothing', () => {
    const p = new SseParser()
    expect(p.push('retry: 3000\n\n')).toEqual([])
    expect(p.retry).toBe(3000)
    p.push('retry: 2s\n\n')
    expect(p.retry).toBe(3000)
    expect(p.push('id: 9\nevent: x\n\ndata: y\n\n')).toEqual([{ id: null, event: 'message', data: 'y' }])
  })

  test('a CR at the end of one chunk and LF at the start of the next are one line end', () => {
    const p = new SseParser()
    expect(p.push('data: a\r')).toEqual([])
    expect(p.push('\n')).toEqual([])
    expect(p.push('\r')).toEqual([{ id: null, event: 'message', data: 'a' }])
    expect(p.push('\ndata: b\r\n\r\n')).toEqual([{ id: null, event: 'message', data: 'b' }])
  })

  test('several frames in one chunk', () => {
    expect(parseChunks([enc.encode(recorded)])).toEqual({ frames: expected, retry: 3000, comments: 2 })
  })

  for (const [name, text] of [
    ['LF', recorded],
    ['CRLF', recorded.replaceAll('\n', '\r\n')],
    ['CR', recorded.replaceAll('\n', '\r')],
  ] as const) {
    test(`the same ${name} stream split at every pair of byte positions parses identically`, () => {
      const bytes = enc.encode(text)
      const want = { frames: expected, retry: 3000, comments: 2 }
      expect(parseChunks([bytes])).toEqual(want)
      for (let i = 0; i <= bytes.length; i++) {
        for (let j = i; j <= bytes.length; j++) {
          const got = parseChunks([bytes.subarray(0, i), bytes.subarray(i, j), bytes.subarray(j)])
          if (JSON.stringify(got) !== JSON.stringify(want)) {
            expect(got, `split at ${i} and ${j}`).toEqual(want)
          }
        }
      }
      expect(parseChunks(Array.from(bytes, (b) => Uint8Array.of(b)))).toEqual(want)
    })
  }

  test('frames complete exactly when their blank line arrives, at every split', () => {
    const bytes = enc.encode(recorded)
    const ends: number[] = []
    for (let k = 0; k < recorded.length; k++) {
      if (recorded.startsWith('\n\n', k)) ends.push(enc.encode(recorded.slice(0, k + 2)).length)
    }
    for (let i = 0; i <= bytes.length; i++) {
      const p = new SseParser()
      const dec = new TextDecoder()
      const first = p.push(dec.decode(bytes.subarray(0, i), { stream: true }))
      // Frames with data whose blank line lies within the first i bytes.
      const complete = ends.filter((e) => e <= i).length
      // Blank lines in order: retry, resync, #13, ping, #14, ping.
      const dataFramesBefore = [0, 0, 1, 2, 2, 3, 3][complete]
      expect(first.length, `split at ${i}`).toBe(dataFramesBefore)
      expect(first).toEqual(expected.slice(0, first.length))
    }
  })
})
