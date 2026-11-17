// A dialog shown from code must live in the app's tree, where it gets the
// session, the board and the lock flag and goes away with its owner: only
// through mountDialog (components/ui/mountDialog.ts). A second app made with
// createApp, or a vnode rendered with Vue's render() into a node of its own,
// has none of that; main.ts is the one place that creates the app.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { expect, test } from 'vitest'

const SRC = __dirname
const ALLOWED = new Set(['main.ts'])

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n)
    if (statSync(p).isDirectory()) return files(p)
    return /\.(ts|vue)$/.test(n) && !/\.test\.ts$/.test(n) ? [p] : []
  })
}

test('no app is created and nothing is rendered outside the app, except in main.ts', () => {
  const bad: string[] = []
  for (const f of files(SRC)) {
    const rel = relative(SRC, f).split('\\').join('/')
    if (ALLOWED.has(rel)) continue
    const text = readFileSync(f, 'utf8')
    text.split('\n').forEach((line, i) => {
      if (/\bcreateApp\s*\(/.test(line) || /\bcreateSSRApp\s*\(/.test(line)) bad.push(`${rel}:${i + 1}: ${line.trim()}`)
    })
    // `render` imported from vue mounts a vnode outside the app's tree.
    for (const m of text.matchAll(/import\s*\{([^}]*)\}\s*from\s*['"]vue['"]/g)) {
      if (/(^|[\s,])render(\s|,|$)/.test(m[1]!)) bad.push(`${rel}: imports render from vue`)
    }
  }
  expect(bad).toEqual([])
})
