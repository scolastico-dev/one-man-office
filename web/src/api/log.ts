// The only place the app writes to the console. It never receives the error
// object itself, which may carry a request, its headers or the login token:
// only the error's code and message are logged, with anything shaped like a
// token removed.

const TOKENISH = /[0-9a-fA-F]{64}/g

function text(v: unknown): string | undefined {
  return typeof v === 'string' ? v.replace(TOKENISH, '[redacted]') : undefined
}

/** What gets logged of an error: its `code` and `message`, when they are text. */
export function describeError(e: unknown): string {
  if (typeof e === 'string') return text(e) ?? ''
  if (typeof e !== 'object' || e === null) return String(e)
  let code: string | undefined
  let message: string | undefined
  try {
    code = text((e as { code?: unknown }).code)
    message = text((e as { message?: unknown }).message)
  } catch {
    // A getter that throws: log what is known.
  }
  if (code !== undefined && message !== undefined) return `${code}: ${message}`
  return message ?? code ?? 'unknown error'
}

/** Logs an error that has no other place to go. Never throws. */
export function logError(context: string, e: unknown): void {
  try {
    console.error(`omo: ${context}: ${describeError(e)}`)
  } catch {
    // Logging must never break the caller.
  }
}
