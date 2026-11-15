import { describe, expect, test } from 'vitest'
import { TerminalInput } from './terminalInput'

function fixture(readyState = 1) {
  const frames: Uint8Array[] = []
  const errors: string[] = []
  const socket = {
    readyState,
    send(data: Uint8Array) {
      frames.push(new Uint8Array(data))
    },
  }
  return { frames, errors, socket, input: new TerminalInput(socket, (t) => errors.push(t)) }
}

const concat = (parts: Uint8Array[]) => {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0))
  let at = 0
  for (const p of parts) {
    out.set(p, at)
    at += p.length
  }
  return out
}

describe('TerminalInput', () => {
  test('a large Unicode paste is paced by acknowledgements and followed by Enter', () => {
    const { input, frames, errors } = fixture()
    const paste = '\x1b[200~' + 'ä🎉\rtext'.repeat(20000) + '\x1b[201~'
    input.send(paste)
    input.send('\r')
    expect(frames).toHaveLength(1)
    expect(frames[0]!.length).toBe(16 * 1024)
    while (input.pendingBytes) input.acknowledge()
    expect(frames.every((f) => f.length <= 16 * 1024)).toBe(true)
    expect(concat(frames)).toEqual(new TextEncoder().encode(paste + '\r'))
    expect(errors).toEqual([])
    expect(input.queue).toHaveLength(0)
  })

  test('a paste before the socket opens waits instead of disappearing', () => {
    const { input, socket, frames } = fixture(0)
    input.send('pending')
    expect(frames).toHaveLength(0)
    socket.readyState = 1
    input.flush()
    expect(new TextDecoder().decode(frames[0])).toBe('pending')
    input.acknowledge()
    expect(input.pendingBytes).toBe(0)
  })

  test('oversized and excessive queued inputs are rejected whole without disconnecting', () => {
    const { input, socket, frames, errors } = fixture()
    input.send('🎉'.repeat(1024 * 1024 + 1))
    expect(frames).toHaveLength(0)
    expect(errors).toHaveLength(1)
    input.send('valid')
    expect(new TextDecoder().decode(frames[0])).toBe('valid')
    for (let i = 0; i < 1024; i++) input.send('x')
    expect(input.queue).toHaveLength(1024)
    expect(errors).toHaveLength(2)
    expect(socket.readyState).toBe(1)
  })

  test('a disconnect discards pending input and late acknowledgements cannot replay it', () => {
    const { input, frames, errors } = fixture()
    input.send('x'.repeat(70000))
    input.close()
    input.acknowledge()
    input.flush()
    input.send('more')
    expect(input.pendingBytes).toBe(0)
    expect(input.queue).toHaveLength(0)
    expect(frames).toHaveLength(1)
    expect(errors[0]).toMatch(/disconnected/)
  })
})
