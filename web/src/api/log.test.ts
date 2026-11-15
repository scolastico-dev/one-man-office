import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { afterEach, describe, expect, test, vi } from 'vitest'
import { ApiError } from './client'
import { describeError, logError } from './log'

const TOKEN = 'abababababababababababababababababababababababababababababababab'

afterEach(() => vi.restoreAllMocks())

describe('logError', () => {
  test('logs only the code and message, as one string', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const e = Object.assign(new ApiError(409, 'invalid_transition', 'No.'), { request: { headers: { Authorization: `Bearer ${TOKEN}` } } })
    logError('handler', e)
    expect(spy).toHaveBeenCalledTimes(1)
    const args = spy.mock.calls[0]!
    expect(args).toEqual(['omo: handler: invalid_transition: No.'])
    expect(JSON.stringify(args)).not.toContain(TOKEN)
  })

  test('removes a token from the message and handles odd values', () => {
    expect(describeError(new Error(`bad ${TOKEN}`))).toBe('bad [redacted]')
    expect(describeError({ token: TOKEN })).toBe('unknown error')
    expect(describeError(null)).toBe('null')
    expect(describeError('plain')).toBe('plain')
    const hostile = {
      get message(): string {
        throw new Error('no')
      },
    }
    expect(describeError(hostile)).toBe('unknown error')
  })

  test('never throws, even when the console does', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {
      throw new Error('console gone')
    })
    expect(() => logError('x', new Error('y'))).not.toThrow()
  })
})

describe('console use', () => {
  test('logError is the only console call in the app outside tests', () => {
    const root = resolve(import.meta.dirname, '..')
    const files: string[] = []
    const walk = (dir: string) => {
      for (const name of readdirSync(dir)) {
        const p = join(dir, name)
        if (statSync(p).isDirectory()) walk(p)
        else if (/\.(ts|js|vue)$/.test(name) && !/\.test\.ts$/.test(name)) files.push(p)
      }
    }
    walk(root)
    expect(files.length).toBeGreaterThan(20)
    const offenders: string[] = []
    for (const f of files) {
      const rel = relative(root, f)
      if (rel === join('api', 'log.ts')) continue
      readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (/\bconsole\s*(\.|\[)/.test(line)) offenders.push(`${rel}:${i + 1}: ${line.trim()}`)
        })
    }
    expect(offenders).toEqual([])
  })
})
