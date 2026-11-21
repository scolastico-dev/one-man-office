import { readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'
import { microtaskGuesses } from './completionGuard'
import { sourceInventory } from './testing/sourceInventory'

const root = join(__dirname, '..')
const files = sourceInventory(join(root, 'src'), join(root, 'e2e'))

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

  it('retains the complete web source and browser test inventory', () => {
    expect(files.length).toBeGreaterThan(100)
  })

  // Each source is an independent assertion. Parser cost must not accumulate
  // across the entire repository under a single test's execution limit.
  it.each(files.map((file) => [relative(root, file), file] as const))('keeps %s free of microtask-count completion guesses', (_name, file) => {
    expect(microtaskGuesses(readFileSync(file, 'utf8')), file).toEqual([])
  })
})
