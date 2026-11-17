// The tag of a listed plugin is decided in one place, trustTagOf, which
// knows where the plugin comes from (omo's own list or an additional one).
// No other code of the page reads a catalog plugin's tag for a badge or an
// install.
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from 'vitest'

test('the tag of a listed plugin is read only through trustTagOf', () => {
  const bad: string[] = []
  for (const n of readdirSync(__dirname)) {
    if (!/\.(ts|vue)$/.test(n) || /\.test\.ts$/.test(n)) continue
    readFileSync(join(__dirname, n), 'utf8')
      .split('\n')
      .forEach((line, i) => {
        // p.tag, plugin.tag, props.plugin.tag … of a CatalogPlugin.
        if (/\b(p|plugin|entry|item)\.tag\b/.test(line) && !(n === 'pluginTrust.ts' && line.includes("p.tag === 'official' || p.tag === 'recommended'"))) bad.push(`${n}:${i + 1}: ${line.trim()}`)
      })
  }
  expect(bad).toEqual([])
})
