// @vitest-environment node
import { readdirSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

// Every place under web/ that starts a program, with how its process ends and why that is enough. A file that
// starts a program without being listed here fails, so a new launcher gets its lifecycle decided in review.
//
// The class this guards: a launcher that starts a *launcher* (`go test`, `go run`, `npm`, `npx`, a shell) and
// ends only its direct child. On Windows `child.kill` ends that child alone, and the program that owns the
// servers or files keeps running. A launcher of a long-lived program therefore starts the program itself.
const WEB = join(__dirname, '..')

const INVENTORY: Record<string, string> = {
  'scripts/fixtureProcess.mjs':
    'FIXED. The standalone driver helper: builds the Go test binary once (`go test -c`, awaited to completion, ended by taskkill /T /F on Windows because the go command starts compilers itself) and runs the binary directly, so the child held is the process that owns the servers. Closed and awaited on stop, on every start failure and on interrupt; the build folder is removed afterwards.',
  'e2e/support/run.ts':
    'RETAINED, hardened. omo CLI commands and the seeding tool, started directly as a binary (no intermediary), awaited to completion; a run past its limit is killed and the rejection comes only after the process is closed.',
  'e2e/support/omo.ts':
    'RETAINED. The omo server binary under test, started directly; stop() sends SIGTERM and escalates to SIGKILL, and awaits the exit. The agents omo itself starts belong to the product (graceful stop and recovery in internal/engine); containment of them at launch on Windows is product work outside this class.',
  'e2e/global-setup.ts': 'RETAINED. execFileSync `go build` of test tools: synchronous, returns only after the go command and its compilers ended; nothing is left running.',
  'e2e/support/editor.ts': 'RETAINED. execFileSync openssl (a prerequisite program of the plugin-editor spec) to make a throw-away certificate: synchronous, returns only after openssl ended; nothing is left running.',
  'e2e/support/git.ts': 'RETAINED. execFileSync git: synchronous and short, no long-lived child.',
  'e2e/support/workspace.ts': 'RETAINED. execFileSync git: synchronous and short, no long-lived child.',
  'e2e/support/plugin.ts': 'RETAINED. execFileSync git: synchronous and short, no long-lived child.',
  'e2e/readme/readme.shots.ts': 'RETAINED. execFileSync git for README art; synchronous.',
  'scripts/readme-art.ts': 'RETAINED. execFileSync pdftocairo for README art; synchronous.',
}

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    if (e.name.startsWith('.') || e.name === 'node_modules' || e.name === 'dist') return []
    const path = join(dir, e.name)
    if (e.isDirectory()) return sources(path)
    return /\.(ts|mts|mjs|js|vue)$/.test(e.name) ? [path] : []
  })
}

const SPAWNED_LAUNCHER = /\bspawn\(\s*['"`](go|npm|npx|pnpm|yarn|sh|bash|zsh|cmd|cmd\.exe|powershell|pwsh)['"`]/
const SHELL_OPTION = /shell:\s*(true|['"`])/
// The calls that leave a child running after they return (or go through a shell).
const LONG_LIVED = /(?<![.\w])(spawn|fork|exec|execSync|execFile)\(/
const IMPORTS_LONG_LIVED = /import\s*\{[^}]*\b(spawn|fork|exec|execSync|execFile)\b[^}]*\}\s*from\s*'(node:)?child_process'/
const LONG_LIVED_HINT = 'starts a program that can outlive the call; use the synchronous forms, or the helper that owns its child'
const SHELL_EXEC = /(?<![.\w])(exec|execSync)\(/

// Files exempt from the checks, each for a stated reason. Nothing else is: specs and unit tests are checked too.
const EXEMPT: Record<string, string> = {
  'src/processOwnership.guard.test.ts': 'this guard: it names launchers as sample text',
  'src/testing/fixtureProcess.test.ts': 'exercises the helper with real stand-in children (node, the built Go test binary), each ended and awaited in the test itself; the helper refuses launchers as its command',
  'src/testing/runProgram.test.ts': 'exercises the program runner with a real stand-in child that is killed and awaited by the runner',
}

const all = sources(WEB).map((p) => ({ path: relative(WEB, p).split('\\').join('/'), text: readFileSync(p, 'utf8') }))
const imports = (f: { text: string }) => /from 'node:child_process'|require\('node:child_process'\)|from 'child_process'/.test(f.text)
const importing = all.filter(imports).filter((f) => !(f.path in EXEMPT))
const isTest = (path: string) => /\.(test|spec)\.(ts|mjs)$/.test(path)
// Production tooling (the helpers and drivers): each is inventoried. Specs and unit tests: one rule, below.
const launchers = importing.filter((f) => !isTest(f.path))
const testFiles = importing.filter((f) => isTest(f.path))
// Everything that starts a program, tests included: the shape checks run on all of it.
const checked = importing

describe('programs started by the web tooling', () => {
  it('lists every tooling file that imports child_process, with its lifecycle', () => {
    expect(launchers.map((f) => f.path).sort()).toEqual(Object.keys(INVENTORY).sort())
  })

  it('has every exemption in place, with a reason', () => {
    for (const [path, why] of Object.entries(EXEMPT)) {
      expect(all.map((f) => f.path), path).toContain(path)
      expect(why.length, path).toBeGreaterThan(20)
    }
  })

  // A spec or unit test that runs a program waits for it: execFileSync/spawnSync/execFile-style synchronous calls only.
  // A long-lived child of a test is the helper's job (fixtureProcess.mjs) or run.ts's.
  it('lets a spec or unit test start programs only synchronously', () => {
    expect(testFiles.length).toBeGreaterThan(0)
    for (const f of testFiles) {
      expect(f.text, `${f.path}: ${LONG_LIVED_HINT}`).not.toMatch(LONG_LIVED)
      expect(f.text, `${f.path}: ${LONG_LIVED_HINT}`).not.toMatch(IMPORTS_LONG_LIVED)
    }
  })

  it('gives every entry a reason', () => {
    for (const [path, why] of Object.entries(INVENTORY)) expect(why.length, path).toBeGreaterThan(40)
  })

  // A long-lived child is started with spawn; only the helper and the runner of the test binaries may do that.
  it('starts a long-lived child (spawn) only in the files that own their child to its close', () => {
    const spawners = checked.filter((f) => /\bspawn\(/.test(f.text)).map((f) => f.path).sort()
    expect(spawners).toEqual(['e2e/support/omo.ts', 'e2e/support/run.ts', 'scripts/fixtureProcess.mjs'])
  })

  it('never starts a launcher (go, npm, npx, a shell) as a long-lived child, nor uses a shell option', () => {
    for (const f of checked) {
      expect(f.text, `${f.path}: spawn of a launcher`).not.toMatch(SPAWNED_LAUNCHER)
      expect(f.text, `${f.path}: shell option`).not.toMatch(SHELL_OPTION)
      expect(f.text, `${f.path}: exec through a shell`).not.toMatch(SHELL_EXEC)
    }
  })

  // `go` appears in fixtureProcess.mjs only as the awaited build; it is never the held child.
  it('lets fixtureProcess.mjs start `go` only as the build that is awaited and run the built binary directly', () => {
    const text = launchers.find((f) => f.path === 'scripts/fixtureProcess.mjs')!.text
    expect([...text.matchAll(/launch\('go'/g)]).toHaveLength(1)
    expect(text).toMatch(/launch\('go', \['test', '-c', '-o'/)
    expect(text).not.toMatch(/'go',\s*\n?\s*args:|command = 'go'|command: 'go'/)
    expect(text).not.toMatch(/'-count=1', '-v'/)
  })

  it('recognises each form of the class in a sample, and not an unrelated exec', () => {
    expect("spawn('go', ['test'])").toMatch(SPAWNED_LAUNCHER)
    expect('spawn("npx", [])').toMatch(SPAWNED_LAUNCHER)
    expect("spawn(binary, [])").not.toMatch(SPAWNED_LAUNCHER)
    expect("spawn(cmd, [], { shell: true })").toMatch(SHELL_OPTION)
    expect("execSync('go test')").toMatch(SHELL_EXEC)
    expect('const m = /x/.exec(text)').not.toMatch(SHELL_EXEC)
    expect("spawn('go', ['run', '.'])").toMatch(LONG_LIVED)
    expect("execFile('npx', [])").toMatch(LONG_LIVED)
    expect("execFileSync('git', ['status'])").not.toMatch(LONG_LIVED)
    expect("spawnSync(process.execPath, [])").not.toMatch(LONG_LIVED)
    expect("import { spawn, execFileSync } from 'node:child_process'").toMatch(IMPORTS_LONG_LIVED)
    expect("import { execFileSync } from 'node:child_process'").not.toMatch(IMPORTS_LONG_LIVED)
  })
})
