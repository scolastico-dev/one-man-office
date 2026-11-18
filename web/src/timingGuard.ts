import ts from 'typescript'

/** Locate direct Playwright fixed waits without matching comments or examples. */
export function fixedBrowserWaits(source: string): number[] {
  const file = ts.createSourceFile('test.ts', source, ts.ScriptTarget.Latest, true)
  const positions: number[] = []
  const visit = (node: ts.Node) => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)
      && node.expression.name.text === 'waitForTimeout') positions.push(node.getStart(file))
    ts.forEachChild(node, visit)
  }
  visit(file)
  return positions
}
