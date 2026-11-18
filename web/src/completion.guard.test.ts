import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { microtaskGuesses } from './completionGuard'

describe('completion guess guard', () => {
  it('rejects a fixed number of microtask turns', () => {
    expect(microtaskGuesses('async function t() { for (let i = 0; i < 30; i++) await Promise.resolve()}')).toHaveLength(1)
    expect(microtaskGuesses('async function t() { for (let i = 0; i < 5; i++) { await null }}')).toHaveLength(1)
    expect(microtaskGuesses('async function t() { for (let i = 0; i < 5; i++) { await (undefined) }}')).toHaveLength(1)
    expect(microtaskGuesses('async function t() { async function f() { await Promise.resolve(); await Promise.resolve(); await Promise.resolve() }}')).toHaveLength(1)
  })

  it('allows macrotask barriers, conditions and unrelated loops', () => {
    expect(microtaskGuesses('async function t() { await new Promise((r) => setImmediate(r))}')).toEqual([])
    expect(microtaskGuesses('async function t() { await Promise.resolve(); await x(); await Promise.resolve()}')).toEqual([])
    expect(microtaskGuesses('async function t() { for (let i = 0; i < 30; i++) items.push(i)}')).toEqual([])
    expect(microtaskGuesses('async function t() { for (let i = 0; i < 30; i++) await send(i)}')).toEqual([])
    expect(microtaskGuesses('async function t() { while (!ready()) await new Promise((r) => setTimeout(r, 1))}')).toEqual([])
    expect(microtaskGuesses('async function t() { for (let i = 0; i < 3; i++) { const g = async () => { await Promise.resolve() }; g }}')).toEqual([])
  })

  it('keeps web sources and browser tests free of microtask-count completion guesses', () => {
    const root = join(__dirname, '..')
    let checked = 0
    const visit = (dir: string) => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        if (entry.name.startsWith('.') || ['node_modules', 'dist', 'test-results', 'playwright-report'].includes(entry.name)) continue
        const path = join(dir, entry.name)
        if (entry.isDirectory()) visit(path)
        else if (/\.(ts|js|mjs)$/.test(entry.name)) {
          checked++
          expect(microtaskGuesses(readFileSync(path, 'utf8')), path).toEqual([])
        }
      }
    }
    visit(join(root, 'src'))
    visit(join(root, 'e2e'))
    expect(checked).toBeGreaterThan(100)
  })
})
