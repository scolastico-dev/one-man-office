// See UnplacedErrors.vue for the rule this serves.

/**
 * The refusals (by dotted field) that no field of the form shows, as the
 * server's sentences: `placed` lists the fields the form shows, or tells
 * whether it shows one.
 */
export function unplacedErrors(fieldErrors: Readonly<Record<string, string>>, placed: readonly string[] | ((field: string) => boolean)): string[] {
  const isPlaced = typeof placed === 'function' ? placed : (f: string) => placed.includes(f)
  return Object.entries(fieldErrors)
    .filter(([field]) => !isPlaced(field))
    .map(([, message]) => message)
}
