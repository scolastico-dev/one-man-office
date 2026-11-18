/** Only failure to bind this TCP port permits another initial acquisition. */
export function isPortCollision(output: string, port: number): boolean {
  return new RegExp(`listen tcp(?:4|6)? [^\\r\\n]+:${port}: bind: (?:address already in use|only one usage)`, 'i').test(output)
}

/** The address omo printed on a complete line of `output` (its token is empty without login); a line still being written is not one yet. */
export function findAddress(output: string, port: number): { token: string } | undefined {
  const m = new RegExp(`^omo: http://omo\\.localhost:${port}/(?:#([0-9a-f]{64}))?[ \\t]*\\r?\\n`, 'm').exec(output)
  return m ? { token: m[1] ?? '' } : undefined
}
