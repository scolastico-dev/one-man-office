import { beforeEach, describe, expect, test } from 'vitest'
import { isAgentSrc, isWritable, resetWritable, setWritable } from './terminalWritable'

const AGENT = '/api/agents/abc/terminal'
const OTHER_AGENT = '/api/agents/def/terminal'
const SHELL = '/api/terminals/t1/socket'

beforeEach(resetWritable)

describe('terminal writable state', () => {
  test('an agent terminal starts read-only', () => {
    expect(isWritable(AGENT)).toBe(false)
  })

  test('the choice is kept for that terminal only', () => {
    setWritable(AGENT, true)
    expect(isWritable(AGENT)).toBe(true)
    expect(isWritable(OTHER_AGENT)).toBe(false)
    setWritable(AGENT, false)
    expect(isWritable(AGENT)).toBe(false)
  })

  test('it is kept for the browser session: in session storage, not past it', () => {
    setWritable(AGENT, true)
    expect(window.sessionStorage.getItem('omo.terminalWritable')).toContain('abc')
    expect(window.localStorage.getItem('omo.terminalWritable')).toBeNull()
  })

  test('a user shell is always writable and takes no choice', () => {
    expect(isAgentSrc(SHELL)).toBe(false)
    expect(isWritable(SHELL)).toBe(true)
    setWritable(SHELL, false)
    expect(isWritable(SHELL)).toBe(true)
  })

  test('blocked session storage keeps the choice for the page', () => {
    const real = Object.getOwnPropertyDescriptor(window, 'sessionStorage')!
    Object.defineProperty(window, 'sessionStorage', { get: () => { throw new Error('blocked') }, configurable: true })
    try {
      setWritable(AGENT, true)
      expect(isWritable(AGENT)).toBe(true)
    } finally {
      Object.defineProperty(window, 'sessionStorage', real)
    }
  })
})
