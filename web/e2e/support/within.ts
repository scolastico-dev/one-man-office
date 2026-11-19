import { posix, win32 } from 'node:path'

type PlatformPath = typeof posix

/**
 * Whether `child` lies strictly below `parent`, by the path rules of `flavor`
 * (the running platform's by default): backslash or slash separators, drive
 * letters and case on Windows. Both are resolved first, so `..` cannot escape
 * and a sibling that shares a prefix (`/tmp2` next to `/tmp`) is outside.
 * A text comparison with a hard-coded '/' is wrong on Windows, which writes
 * its paths with backslashes.
 */
export function isBelow(parent: string, child: string, flavor: PlatformPath = process.platform === 'win32' ? win32 : posix): boolean {
  const rel = flavor.relative(flavor.resolve(parent), flavor.resolve(child))
  if (rel === '' || flavor.isAbsolute(rel)) return false // the folder itself, or another drive
  return rel !== '..' && !rel.startsWith('..' + flavor.sep)
}
