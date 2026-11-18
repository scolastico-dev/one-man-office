/** Only failure to bind this TCP port permits another initial acquisition. */
export function isPortCollision(output: string, port: number): boolean {
  return new RegExp(`listen tcp(?:4|6)? [^\\r\\n]+:${port}: bind: (?:address already in use|only one usage)`, 'i').test(output)
}

/** The address omo printed on a complete line of `output` (its token is empty without login); a line still being written is not one yet. */
// With public_url configured, the isolated loopback address is an "also"
// line: match that exact host and port instead of assuming it is the primary URL.
export function findAddress(output: string, port: number): { token: string } | undefined {
  const m = new RegExp(`^(?:omo|also): http://omo\\.localhost:${port}/(?:#([0-9a-f]{64}))?[ \\t]*\\r?\\n`, 'm').exec(output)
  return m ? { token: m[1] ?? '' } : undefined
}
