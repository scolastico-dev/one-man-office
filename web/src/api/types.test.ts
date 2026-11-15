import { readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, test } from 'vitest'
import type { EventDataByKind } from './types'

// The server's wire types in internal/api are the source of truth. This test
// reads both sides and fails when a JSON field exists on one and not the other.

const goDir = resolve(import.meta.dirname, '../../../internal/api') + '/'
const tsFile = resolve(import.meta.dirname, 'types.ts')

/** Go struct name to TypeScript interface name where they differ. */
const renamed: Record<string, string> = { Event: 'ApiEvent', Error: 'ErrorBody', UpdateTicket: 'UpdateTicketFields' }
/** Go structs without a JSON form of their own. */
const skipped = new Set(['Optional', 'TicketFilter'])

function goStructs(): Map<string, string[]> {
  const raw = new Map<string, { fields: string[]; embeds: string[] }>()
  for (const f of readdirSync(goDir)) {
    if (!f.endsWith('.go') || f.endsWith('_test.go')) continue
    const src = readFileSync(goDir + f, 'utf8')
    for (const m of src.matchAll(/^type (\w+)(?:\[[^\]]*\])? struct \{\n([\s\S]*?)^\}/gm)) {
      const fields: string[] = []
      const embeds: string[] = []
      for (const line of m[2]!.split('\n')) {
        const tag = /json:"([^",]*)/.exec(line)
        if (tag) {
          if (tag[1] !== '-' && tag[1] !== '') fields.push(tag[1]!)
          continue
        }
        const embed = /^\s+([A-Z]\w*)\s*$/.exec(line)
        if (embed) embeds.push(embed[1]!)
      }
      raw.set(m[1]!, { fields, embeds })
    }
  }
  const out = new Map<string, string[]>()
  const flatten = (name: string): string[] => {
    const s = raw.get(name)
    if (!s) return []
    return [...s.embeds.flatMap(flatten), ...s.fields]
  }
  for (const name of raw.keys()) out.set(name, flatten(name).sort())
  return out
}

function tsInterfaces(): Map<string, string[]> {
  const src = readFileSync(tsFile, 'utf8')
  const raw = new Map<string, { fields: string[]; base: string | undefined }>()
  for (const m of src.matchAll(/^export interface (\w+)(?: extends (\w+))? \{\n([\s\S]*?)^\}/gm)) {
    const fields = [...m[3]!.matchAll(/^ {2}'?([\w.]+)'?\??:/gm)].map((f) => f[1]!)
    raw.set(m[1]!, { fields, base: m[2] })
  }
  const out = new Map<string, string[]>()
  const flatten = (name: string): string[] => {
    const s = raw.get(name)
    if (!s) return []
    return [...(s.base ? flatten(s.base) : []), ...s.fields]
  }
  for (const name of raw.keys()) out.set(name, flatten(name).sort())
  return out
}

describe('wire types', () => {
  test('every JSON field of the server has its field in types.ts and the other way round', () => {
    const go = goStructs()
    const ts = tsInterfaces()
    expect(go.size).toBeGreaterThan(50)
    const mismatches: string[] = []
    for (const [name, fields] of go) {
      if (skipped.has(name)) continue
      const tsName = renamed[name] ?? name
      const tsFields = ts.get(tsName)
      if (tsFields === undefined) {
        mismatches.push(`${name}: no interface ${tsName}`)
        continue
      }
      const missing = fields.filter((f) => !tsFields.includes(f))
      const extra = tsFields.filter((f) => !fields.includes(f))
      if (missing.length > 0) mismatches.push(`${tsName} lacks ${missing.join(', ')}`)
      if (extra.length > 0) mismatches.push(`${tsName} has ${extra.join(', ')} the server does not know`)
    }
    expect(mismatches).toEqual([])
  })

  test('EventDataByKind covers every streamed event kind of the server', () => {
    const src = readFileSync(goDir + 'events.go', 'utf8')
    const kinds = [...src.matchAll(/^\s+Kind\w+\s*=\s*"([^"]+)"/gm)].map((m) => m[1]!).filter((k) => k.includes('.'))
    expect(kinds.length).toBeGreaterThan(15)
    const declared = tsInterfaces().get('EventDataByKind')!
    expect([...declared].sort()).toEqual([...kinds].sort())
    // Type-level check that the two newest kinds carry their data types.
    const retried: EventDataByKind['ticket.retried'] = { ticket: 1, step: 'start' }
    const scheduler: EventDataByKind['scheduler.changed'] = { state: { running: 1, limit: 3, paused: false } }
    expect(retried.step).toBe('start')
    expect(scheduler.state.limit).toBe(3)
  })
})
