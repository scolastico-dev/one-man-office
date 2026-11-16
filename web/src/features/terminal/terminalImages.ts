// Images dropped or pasted into a terminal. Agent programs (Claude Code,
// Codex) take an image when its file's path is pasted, as a file manager
// pastes a copied file: the absolute path, quoted when it holds spaces or
// shell characters, several separated by a space. omo stores each image in
// its images folder (`POST /api/terminal-images`) and pastes the paths the
// server answers; the server decides from the bytes what is an image.

/** The types omo stores; the server checks the bytes again. */
const IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp'])
const IMAGE_NAME = /\.(png|jpe?g|gif|webp)$/i

/** Whether a dropped or pasted file is an image omo takes (by its type, or by its name when the browser gives no type). */
export function isImageFile(file: File): boolean {
  if (file.type !== '') return IMAGE_TYPES.has(file.type)
  return IMAGE_NAME.test(file.name)
}

/** A path as a shell reads it: as it is when it holds only plain characters, else in single quotes. */
export function shellQuote(path: string): string {
  if (/^[A-Za-z0-9_@%+=:,./\\-]+$/.test(path)) return path
  return `'${path.replace(/'/g, `'\\''`)}'`
}

/** The text pasted for stored images: their quoted paths, separated by a space, with no line break. */
export function pastedPaths(paths: readonly string[]): string {
  return paths.map(shellQuote).join(' ')
}
