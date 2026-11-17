/** A commit id as omo shows it: its first 12 characters (git's full id stays in the title). */
export function shortCommit(id: string): string {
  return id.slice(0, 12)
}
