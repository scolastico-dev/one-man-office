import { ApiError } from './client'

/** The request got no answer at all, so the server may or may not have acted on it. */
export function isLostAnswer(e: unknown): boolean {
  return e instanceof ApiError && e.code === 'network'
}

/**
 * The words for a failed write: a lost answer says the change may have
 * happened, anything else is the server's own message (or `refused`).
 */
export function failureText(e: unknown, o: { lost: string; refused: string }): string {
  if (isLostAnswer(e)) return o.lost
  return e instanceof Error && e.message !== '' ? e.message : o.refused
}
