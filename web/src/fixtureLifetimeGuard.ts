import ts from 'typescript'

/**
 * Calls that make something outside the process: a file or folder, a socket, a
 * child process. At module load they run once per worker process and once per
 * `--list`, while the cleanup that pairs with them (a `test.afterAll`) runs once
 * per group of tests, so a second group of the same file in that worker (a repeat,
 * a describe block that runs after the first group ended) finds the resource gone.
 */
export const ALLOCATING_CALLS: ReadonlySet<string> = new Set([
  'mkdtemp',
  'mkdtempSync',
  'mkdir',
  'mkdirSync',
  'writeFile',
  'writeFileSync',
  'appendFile',
  'appendFileSync',
  'copyFile',
  'copyFileSync',
  'cp',
  'cpSync',
  'symlink',
  'symlinkSync',
  'link',
  'linkSync',
  'chmod',
  'chmodSync',
  'open',
  'openSync',
  'createWriteStream',
  'createServer',
  'listen',
  'spawn',
  'spawnSync',
  'exec',
  'execSync',
  'execFile',
  'execFileSync',
  'fork',
])

export interface Allocation {
  /** The call that allocates, as written (`mkdtempSync`, or the helper's name). */
  call: string
  /** 1-based line. */
  line: number
  /** `direct`, or `via helper <name>` for a function of this repository that allocates. */
  how: string
}

const functionLike = (n: ts.Node): n is ts.FunctionLikeDeclaration | ts.ClassLikeDeclaration =>
  ts.isFunctionDeclaration(n) || ts.isFunctionExpression(n) || ts.isArrowFunction(n) || ts.isMethodDeclaration(n) || ts.isConstructorDeclaration(n) || ts.isGetAccessorDeclaration(n) || ts.isSetAccessorDeclaration(n) || ts.isClassDeclaration(n) || ts.isClassExpression(n)

/** `describe`, `test.describe`, `test.describe.serial`, ...: a callback that Playwright runs while it loads the file. */
function isDescribe(call: ts.CallExpression): boolean {
  let e: ts.Expression = call.expression
  while (ts.isPropertyAccessExpression(e)) {
    if (e.name.text === 'describe') return true
    e = e.expression
  }
  return ts.isIdentifier(e) && e.text === 'describe'
}

function calleeName(call: ts.CallExpression): string {
  const e = call.expression
  if (ts.isIdentifier(e)) return e.text
  if (ts.isPropertyAccessExpression(e)) return e.name.text
  return ''
}

/** Names of the functions a source declares, each with the names it calls anywhere in its body. */
export function declaredFunctions(source: string): Map<string, Set<string>> {
  const file = ts.createSourceFile('x.ts', source, ts.ScriptTarget.Latest, true)
  const out = new Map<string, Set<string>>()
  const calls = (body: ts.Node): Set<string> => {
    const names = new Set<string>()
    const visit = (n: ts.Node) => {
      if (ts.isCallExpression(n)) {
        const name = calleeName(n)
        if (name !== '') names.add(name)
      }
      ts.forEachChild(n, visit)
    }
    visit(body)
    return names
  }
  const visit = (n: ts.Node) => {
    if (ts.isFunctionDeclaration(n) && n.name !== undefined && n.body !== undefined) {
      out.set(n.name.text, new Set([...(out.get(n.name.text) ?? []), ...calls(n.body)]))
    } else if (ts.isVariableDeclaration(n) && ts.isIdentifier(n.name) && n.initializer !== undefined && (ts.isArrowFunction(n.initializer) || ts.isFunctionExpression(n.initializer))) {
      out.set(n.name.text, new Set([...(out.get(n.name.text) ?? []), ...calls(n.initializer.body)]))
    }
    ts.forEachChild(n, visit)
  }
  visit(file)
  return out
}

/** Every function name (of the given declarations) that makes something outside the process, directly or through another one of them. */
export function allocatingHelpers(declared: Map<string, Set<string>>): Set<string> {
  const found = new Set<string>()
  for (let changed = true; changed; ) {
    changed = false
    for (const [name, calls] of declared) {
      if (found.has(name)) continue
      if ([...calls].some((c) => ALLOCATING_CALLS.has(c) || found.has(c))) {
        found.add(name)
        changed = true
      }
    }
  }
  return found
}

/**
 * The calls that run while the file loads (outside any function, but inside a
 * describe callback) and make a resource: directly, or through one of `helpers`.
 */
export function moduleLoadAllocations(source: string, helpers: ReadonlySet<string> = new Set()): Allocation[] {
  const file = ts.createSourceFile('x.ts', source, ts.ScriptTarget.Latest, true)
  const found: Allocation[] = []
  const visit = (n: ts.Node) => {
    if (ts.isCallExpression(n)) {
      const name = calleeName(n)
      const line = file.getLineAndCharacterOfPosition(n.getStart(file)).line + 1
      if (ALLOCATING_CALLS.has(name)) found.push({ call: name, line, how: 'direct' })
      else if (helpers.has(name)) found.push({ call: name, line, how: `via helper ${name}` })
      if (isDescribe(n)) {
        // The callback of a describe runs at load: keep walking into it, but not into test bodies and hooks.
        n.arguments.forEach((a) => {
          if ((ts.isArrowFunction(a) || ts.isFunctionExpression(a)) && a.body !== undefined) visit(a.body)
          else visit(a)
        })
        visit(n.expression)
        return
      }
    }
    if (functionLike(n)) return
    ts.forEachChild(n, visit)
  }
  visit(file)
  return found
}

/**
 * Lines where the source calls one of `names` or reads `process.pid`, anywhere
 * (also inside tests): a spec that names its own folders in the shared temporary
 * folder, instead of using the test's home or the `repo` fixture, owns a resource
 * no fixture cleans up and that a second worker or a repeat can share by name.
 */
export function ownTemporaryFolders(source: string): Allocation[] {
  const file = ts.createSourceFile('x.ts', source, ts.ScriptTarget.Latest, true)
  const found: Allocation[] = []
  const at = (n: ts.Node) => file.getLineAndCharacterOfPosition(n.getStart(file)).line + 1
  const visit = (n: ts.Node) => {
    if (ts.isCallExpression(n) && ['tmpdir', 'mkdtemp', 'mkdtempSync'].includes(calleeName(n))) found.push({ call: calleeName(n), line: at(n), how: 'direct' })
    if (ts.isPropertyAccessExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === 'process' && n.name.text === 'pid') found.push({ call: 'process.pid', line: at(n), how: 'direct' })
    ts.forEachChild(n, visit)
  }
  visit(file)
  return found
}
