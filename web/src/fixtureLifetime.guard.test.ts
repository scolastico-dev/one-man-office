import { execFileSync } from 'node:child_process'
import { mkdtempSync, readdirSync, readFileSync, realpathSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { allocatingHelpers, declaredFunctions, moduleLoadAllocations, ownTemporaryFolders } from './fixtureLifetimeGuard'

const e2e = join(__dirname, '..', 'e2e')
const sources = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    if (e.name.startsWith('.') || e.name === 'node_modules') return []
    const p = join(dir, e.name)
    return e.isDirectory() ? sources(p) : /\.ts$/.test(e.name) ? [p] : []
  })

describe('fixtures are not made when a browser test file loads', () => {
  it('finds a resource made at load, directly, through a helper and inside a describe', () => {
    const direct = `const dir = mkdtempSync(join(tmpdir(), 'x-'))\ntest.afterAll(() => rmSync(dir))`
    expect(moduleLoadAllocations(direct).map((a) => [a.call, a.line, a.how])).toEqual([['mkdtempSync', 1, 'direct']])

    const helper = `function fake() { return mkdtempSync('x') }\nconst FAKE = fake()`
    const helpers = allocatingHelpers(declaredFunctions(helper))
    expect([...helpers]).toEqual(['fake'])
    expect(moduleLoadAllocations(helper, helpers).map((a) => a.how)).toEqual(['via helper fake'])

    const chained = `const a = () => b()\nfunction b() { writeFileSync('f', '') }\nconst x = a()`
    expect(moduleLoadAllocations(chained, allocatingHelpers(declaredFunctions(chained))).map((a) => a.call)).toEqual(['a'])

    const described = `test.describe('g', () => {\n  symlinkSync('a', 'b')\n  test('t', () => { mkdtempSync('x') })\n})`
    expect(moduleLoadAllocations(described).map((a) => a.call)).toEqual(['symlinkSync'])
  })

  it('accepts constants, names and resources made inside tests, hooks and fixtures', () => {
    const fine = `
      const NAME = join(tmpdir(), \`omo-\${process.pid}\`)
      const PNG = Buffer.from('aGk=', 'base64')
      const SCRIPT = [ '#!/bin/sh', 'exit 0' ].join('\\n')
      function fake() { return mkdtempSync('x') }
      test.beforeAll(() => mkdirSync(NAME))
      test('t', async () => { const d = fake(); writeFileSync(join(d, 'f'), '') })
      test.use({ omoOptions: { programs: { files: { claude: SCRIPT } } } })
    `
    expect(moduleLoadAllocations(fine, allocatingHelpers(declaredFunctions(fine)))).toEqual([])
  })

  it('keeps every browser test file and its helpers free of allocation at load', () => {
    const files = sources(e2e)
    const declared = new Map<string, Set<string>>()
    for (const f of files) for (const [name, calls] of declaredFunctions(readFileSync(f, 'utf8'))) declared.set(name, new Set([...(declared.get(name) ?? []), ...calls]))
    const helpers = allocatingHelpers(declared)
    const offenders = files.flatMap((f) => moduleLoadAllocations(readFileSync(f, 'utf8'), helpers).map((a) => `${f.slice(e2e.length + 1)}:${a.line} ${a.call} (${a.how})`))
    expect(offenders, 'a fixture made at module load outlives or predates the tests that use it; make it a Playwright fixture (see support/omo.ts)').toEqual([])
    expect(files.length).toBeGreaterThan(60)
  })

  it('finds a spec that names its own folder in the shared temporary folder', () => {
    expect(ownTemporaryFolders("const d = join(tmpdir(), `x-${process.pid}`)").map((a) => a.call)).toEqual(['tmpdir', 'process.pid'])
    expect(ownTemporaryFolders("const d = join(dirname(repo), 'second')\n// tmpdir() in a comment\nconst s = 'process.pid'")).toEqual([])
  })

  it('keeps specs on the fixtures: no folder of their own in the shared temporary folder', () => {
    const offenders = sources(e2e)
      .filter((f) => f.endsWith('.spec.ts'))
      .flatMap((f) => ownTemporaryFolders(readFileSync(f, 'utf8')).map((a) => `${f.slice(e2e.length + 1)}:${a.line} ${a.call}`))
    expect(offenders, 'use the test home (omo.home), the repo fixture or omoOptions.programs; they are removed with the test').toEqual([])
  })

  it('lists the tests without creating anything', { timeout: 120_000 }, () => {
    const scratch = realpathSync(mkdtempSync(join(realpathSync(tmpdir()), 'omo-list-guard-')))
    try {
      // An owned environment: no OMO_ variable of the session this runs in, a temporary folder of its own to watch.
      const env: NodeJS.ProcessEnv = {}
      for (const [k, v] of Object.entries(process.env)) if (!k.toUpperCase().startsWith('OMO_')) env[k] = v
      env.TMPDIR = scratch
      env.TEMP = scratch
      env.TMP = scratch
      const out = execFileSync(join(__dirname, '..', 'node_modules', '.bin', 'playwright'), ['test', '--list', '--config', join(__dirname, '..', 'playwright.config.ts')], { cwd: join(__dirname, '..'), env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
      expect(out).toMatch(/Total: \d+ tests/)
      expect(readdirSync(scratch).filter((n) => !n.startsWith('playwright-transform-cache')), 'listing the tests must not make files or folders').toEqual([])
    } finally {
      rmSync(scratch, { recursive: true, force: true })
    }
  })
})
