// Ticket timestamps (`created_at`, `updated_at`, `closed_at`) are Go times
// written as RFC 3339 with nanoseconds and trailing zeros dropped, so their
// width varies ("…:05Z", "…:05.5Z", "…:05.123456789Z") and the text does not
// sort by time. Event timestamps (`ApiEvent.at`) are fixed-width already.

const RFC3339 = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/i

/**
 * A fixed-width UTC form of an RFC 3339 timestamp,
 * `YYYY-MM-DDTHH:MM:SS.nnnnnnnnnZ`, that sorts as text in time order and
 * keeps every digit of the fraction. An offset such as `+02:00` is converted
 * to UTC. Text that is not RFC 3339 is returned unchanged.
 */
export function timeKey(s: string): string {
  const m = RFC3339.exec(s)
  if (m === null) return s
  const [, y, mo, d, h, mi, sec, frac = '', zone = 'Z'] = m
  // Whole seconds only, so Date's millisecond precision loses nothing.
  let ms = Date.UTC(Number(y), Number(mo) - 1, Number(d), Number(h), Number(mi), Number(sec))
  if (Number(y) < 100) ms = new Date(ms).setUTCFullYear(Number(y))
  if (zone.toUpperCase() !== 'Z') {
    const sign = zone.startsWith('-') ? -1 : 1
    ms -= sign * (Number(zone.slice(1, 3)) * 60 + Number(zone.slice(4, 6))) * 60_000
  }
  if (Number.isNaN(ms)) return s
  return `${new Date(ms).toISOString().slice(0, 19)}.${frac.padEnd(9, '0')}Z`
}

/** Compares two ticket timestamps by time; negative when a is earlier. */
export function compareTime(a: string, b: string): number {
  const ka = timeKey(a)
  const kb = timeKey(b)
  return ka < kb ? -1 : ka > kb ? 1 : 0
}
