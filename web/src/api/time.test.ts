import { describe, expect, test } from 'vitest'
import { compareTime, timeKey } from './time'

describe('timeKey', () => {
  test('pads the fraction to nine digits and adds one where it is missing', () => {
    expect(timeKey('2026-11-15T10:00:05Z')).toBe('2026-11-15T10:00:05.000000000Z')
    expect(timeKey('2026-11-15T10:00:05.5Z')).toBe('2026-11-15T10:00:05.500000000Z')
    expect(timeKey('2026-11-15T10:00:05.123456789Z')).toBe('2026-11-15T10:00:05.123456789Z')
  })

  test('converts an offset to UTC and keeps sub-millisecond digits', () => {
    expect(timeKey('2026-11-15T12:00:05.0000001+02:00')).toBe('2026-11-15T10:00:05.000000100Z')
    expect(timeKey('2026-01-01T00:30:00-01:00')).toBe('2026-01-01T01:30:00.000000000Z')
    expect(timeKey('2026-01-01T00:30:00+00:00')).toBe('2026-01-01T00:30:00.000000000Z')
  })

  test('leaves text that is not RFC 3339 alone', () => {
    expect(timeKey('')).toBe('')
    expect(timeKey('x')).toBe('x')
  })
})

describe('compareTime', () => {
  test('orders the server format by value, where text order is wrong', () => {
    // As text, "…:05Z" sorts after "…:05.5Z" and "…:05.1Z" after "…:05.12Z".
    expect(compareTime('2026-11-15T10:00:05Z', '2026-11-15T10:00:05.5Z')).toBeLessThan(0)
    expect(compareTime('2026-11-15T10:00:05.1Z', '2026-11-15T10:00:05.12Z')).toBeLessThan(0)
    expect(compareTime('2026-11-15T10:00:05.000000001Z', '2026-11-15T10:00:05.000000002Z')).toBeLessThan(0)
    expect(compareTime('2026-11-15T10:00:05.1Z', '2026-11-15T10:00:05.100Z')).toBe(0)
    expect(compareTime('2026-11-15T11:00:00+02:00', '2026-11-15T10:00:00Z')).toBeLessThan(0)
  })
})
