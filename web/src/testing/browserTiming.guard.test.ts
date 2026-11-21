import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import ts from 'typescript'
import { expect, test } from 'vitest'

// Browser actions must wait on product state; scripted agents must be released
// by the test, rather than giving the browser a head start of N milliseconds.
const root = resolve('e2e')
const sources = (dir: string) => readdirSync(dir, { recursive: true, encoding: 'utf8' }).filter((name) => name.endsWith('.ts'))
const files = sources(root)

test.each(files)('browser tests never use wall-clock sleeps as completion barriers: %s', (name) => {
  const failures: string[] = []
  const text = readFileSync(resolve(root, name), 'utf8')
  for (const [i, line] of text.split('\n').entries()) {
    if (/\.waitForTimeout\s*\(|['"`]sleep\|/.test(line)) failures.push(`${name}:${i + 1}: ${line.trim()}`)
  }
  expect(failures).toEqual([])
})

function absentLoaderAssertions(name: string, text: string): string[] {
  const failures: string[] = []
  const source = ts.createSourceFile(name, text, ts.ScriptTarget.Latest, true)
  // Parse assertion chains so line breaks, spaces and expect.soft cannot
  // conceal a loader-absence barrier in a spec or a shared helper.
  function visit(node: ts.Node): void {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)) {
      const matcher = node.expression.name.text
      let receiver = node.expression.expression
      const negated = ts.isPropertyAccessExpression(receiver) && receiver.name.text === 'not'
      if (negated && ts.isPropertyAccessExpression(receiver)) receiver = receiver.expression
      const absent = (matcher === 'toBeHidden' && !negated)
        || (matcher === 'toBeVisible' && negated)
        || (matcher === 'toHaveCount' && !negated && node.arguments[0]?.getText(source) === '0')
      if (absent && ts.isCallExpression(receiver)
        && /^(expect|expect\.soft)$/.test(receiver.expression.getText(source))
        && receiver.arguments.some((arg) => /\[data-(?:[\w-]+-)?loading(?=[\s=\]])/.test(arg.getText(source)))) {
        failures.push(`${name}:${source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1}`)
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return failures
}

// API/file completion, selection geometry and decoded image dimensions need
// behavioral guards: terminal-keys holds saves, terminal-clipboard holds upload
// responses and exercises selection, and images checks decoded naturalWidth.
// Their barriers can span helpers and product events; a generic source pattern
// cannot establish that a particular read or measurement follows completion.
test.each(files)('browser readiness never depends on the absence of a loader: %s', (name) => {
  expect(absentLoaderAssertions(name, readFileSync(resolve(root, name), 'utf8'))).toEqual([])
})

test('loader guard covers nested helpers, every loader and multiline negative assertions', () => {
  const root = mkdtempSync(join(tmpdir(), 'omo-timing-guard-'))
  try {
    mkdirSync(join(root, 'support', 'nested'), { recursive: true })
    writeFileSync(join(root, 'support', 'nested', 'readiness.ts'), `
      await expect(page.locator('[data-settings-loading]')).toHaveCount(0)
      await expect(
        page.locator('[data-projects-loading]')
      ).toBeHidden()
      await expect(page.locator('[data-agent-loading]'))
        .not.toBeVisible()
      await expect.soft(page.locator('[data-loading]')).toHaveCount(
        0
      )
      await expect(page.locator('[data-settings-loading]')).toBeVisible()
      await expect(page.locator('[data-projects]')).toHaveCount(0)
    `)
    const failures = sources(root).flatMap((name) => absentLoaderAssertions(name, readFileSync(resolve(root, name), 'utf8')))
    expect(failures).toHaveLength(4)
    expect(failures.every((failure) => failure.startsWith(join('support', 'nested', 'readiness.ts') + ':'))).toBe(true)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
