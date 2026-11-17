// The helper list of the Usage page names every helper a usage script can
// call: those the usage runner registers and those every Lua box has.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from 'vitest'
import { HELPERS } from './usageForms'

const ROOT = join(__dirname, '../../../../..')
const registered = (file: string) => [...readFileSync(join(ROOT, file), 'utf8').matchAll(/\b(?:h\.box|b)\.Set\("([a-z_0-9]+)"/g)].map((m) => m[1]!)

test('every helper omo registers for usage scripts is listed on the Usage page', () => {
  const names = [...registered('internal/usage/helpers.go'), ...registered('internal/luabox/box.go')]
  expect(names).toContain('rate_limited')
  const listed = HELPERS.map(([call]) => call).join(' ')
  for (const name of names) expect(listed, `omo.${name} is missing`).toMatch(new RegExp(`\\bomo\\.${name}\\b`))
})
