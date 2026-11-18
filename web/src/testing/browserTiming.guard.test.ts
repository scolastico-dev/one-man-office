import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import ts from 'typescript'
import { expect, test } from 'vitest'

// Browser actions must wait on product state; scripted agents must be released
// by the test, rather than giving the browser a head start of N milliseconds.
test('browser tests never use wall-clock sleeps as completion barriers', () => {
  const root = resolve('e2e')
  const failures: string[] = []
  for (const name of readdirSync(root, { recursive: true, encoding: 'utf8' })) {
    if (!name.endsWith('.ts')) continue
    const text = readFileSync(resolve(root, name), 'utf8')
    for (const [i, line] of text.split('\n').entries()) {
      if (/\.waitForTimeout\s*\(|['"`]sleep\|/.test(line)) failures.push(`${name}:${i + 1}: ${line.trim()}`)
    }
  }
  expect(failures).toEqual([])
})

function absentLoaderAssertions(root: string): string[] {
  const failures: string[] = []
  for (const name of readdirSync(root, { recursive: true, encoding: 'utf8' })) {
    if (!name.endsWith('.ts')) continue
    const source = ts.createSourceFile(name, readFileSync(resolve(root, name), 'utf8'), ts.ScriptTarget.Latest, true)
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
  }
  return failures
}

// API/file completion, selection geometry and decoded image dimensions need
// behavioral guards: terminal-keys holds saves, terminal-clipboard holds upload
// responses and exercises selection, and images checks decoded naturalWidth.
// Their barriers can span helpers and product events; a generic source pattern
// cannot establish that a particular read or measurement follows completion.
test('browser readiness never depends on the absence of a loader', () => {
  expect(absentLoaderAssertions(resolve('e2e'))).toEqual([])
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
    const failures = absentLoaderAssertions(root)
    expect(failures).toHaveLength(4)
    expect(failures.every((failure) => failure.startsWith(join('support', 'nested', 'readiness.ts') + ':'))).toBe(true)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
