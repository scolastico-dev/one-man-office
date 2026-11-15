import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from 'vitest'
import { EXPORT_FIELDS, PROJECT_FIELDS, TEMPLATE_FIELDS } from './templateFields'

const PROMPTS = join(import.meta.dirname, '..', '..', '..', '..', '..', 'internal', 'prompts')

/** The field names of a Go struct in prompts.go. */
function structFields(name: string): string[] {
  const src = readFileSync(join(PROMPTS, 'prompts.go'), 'utf8')
  const m = new RegExp(`type ${name} struct \\{([\\s\\S]*?)\\n\\}`).exec(src)
  expect(m, `type ${name} in prompts.go`).not.toBeNull()
  const out: string[] = []
  for (const raw of m![1]!.split('\n')) {
    const line = raw.replace(/\/\/.*$/, '').trim()
    const f = /^([A-Z]\w*(?:\s*,\s*[A-Z]\w*)*)\s+\S+/.exec(line)
    if (f) out.push(...f[1]!.split(',').map((x) => x.trim()))
  }
  return out
}

test('the field list names exactly the fields of the template data', () => {
  expect(TEMPLATE_FIELDS.map((f) => f.name).sort()).toEqual(structFields('Data').sort())
  expect(PROJECT_FIELDS.map((f) => f.name).sort()).toEqual(structFields('Project').sort())
  expect(EXPORT_FIELDS.map((f) => f.name).sort()).toEqual(structFields('ExportData').sort())
  for (const f of [...TEMPLATE_FIELDS, ...PROJECT_FIELDS, ...EXPORT_FIELDS]) expect(f.text).not.toBe('')
  const exportText = TEMPLATE_FIELDS.find((f) => f.name === 'Export')!.text
  for (const f of EXPORT_FIELDS) expect(exportText).toContain(`.Export.${f.name}`)
})

test('every field the default prompts use is in the list', () => {
  const dir = join(PROMPTS, 'defaults')
  const known = new Set([...TEMPLATE_FIELDS, ...PROJECT_FIELDS, ...EXPORT_FIELDS].map((f) => f.name))
  const used = new Set<string>()
  for (const file of readdirSync(dir)) {
    const text = readFileSync(join(dir, file), 'utf8')
    for (const action of text.matchAll(/\{\{([\s\S]*?)\}\}/g)) for (const f of action[1]!.matchAll(/(?<![\w)])\.([A-Z]\w*)/g)) used.add(f[1]!)
  }
  expect(used.size).toBeGreaterThan(10)
  expect([...used].filter((f) => !known.has(f))).toEqual([])
})
