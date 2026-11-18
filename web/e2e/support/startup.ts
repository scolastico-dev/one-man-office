/** Only failure to bind this TCP port permits another initial acquisition. */
export function isPortCollision(output: string, port: number): boolean {
  return new RegExp(`listen tcp(?:4|6)? [^\\r\\n]+:${port}: bind: (?:address already in use|only one usage)`, 'i').test(output)
}
