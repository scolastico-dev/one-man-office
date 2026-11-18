import ts from 'typescript'

/**
 * Locate a fixed number of microtask turns used as a completion guess: a loop
 * with a numeric bound whose body awaits `Promise.resolve()`, `null` or
 * `undefined`, or three or more such awaits in a row. After a fixed number of
 * turns "nothing else happened" proves nothing, because a longer promise chain
 * reaches its state later. One real macrotask is the exact point after which the
 * microtask queue is empty (see `flush` in `api/eventStream.test.ts`).
 */
export function microtaskGuesses(source: string): number[] {
  const file = ts.createSourceFile('test.ts', source, ts.ScriptTarget.Latest, true)
  const positions: number[] = []
  const unwrap = (e: ts.Expression): ts.Expression => (ts.isParenthesizedExpression(e) ? unwrap(e.expression) : e)
  const isTurn = (node: ts.Node): boolean => {
    if (!ts.isAwaitExpression(node)) return false
    const e = unwrap(node.expression)
    if (e.kind === ts.SyntaxKind.NullKeyword || (ts.isIdentifier(e) && e.text === 'undefined')) return true
    return (
      ts.isCallExpression(e) &&
      ts.isPropertyAccessExpression(e.expression) &&
      ts.isIdentifier(e.expression.expression) &&
      e.expression.expression.text === 'Promise' &&
      e.expression.name.text === 'resolve'
    )
  }
  const containsTurn = (node: ts.Node): boolean => {
    let found = false
    const visit = (n: ts.Node) => {
      if (found || ts.isFunctionLike(n)) return
      if (isTurn(n)) found = true
      else ts.forEachChild(n, visit)
    }
    visit(node)
    return found
  }
  const hasNumber = (node: ts.Node | undefined): boolean => {
    if (!node) return false
    let found = false
    const visit = (n: ts.Node) => {
      if (ts.isNumericLiteral(n)) found = true
      else ts.forEachChild(n, visit)
    }
    visit(node)
    return found
  }
  const visit = (node: ts.Node) => {
    if (ts.isForStatement(node) && hasNumber(node.condition) && containsTurn(node.statement)) positions.push(node.getStart(file))
    if (ts.isBlock(node) || ts.isSourceFile(node)) {
      let run = 0
      for (const st of node.statements) {
        if (ts.isExpressionStatement(st) && isTurn(st.expression)) {
          if (++run === 3) positions.push(st.getStart(file))
        } else run = 0
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(file)
  return positions
}
