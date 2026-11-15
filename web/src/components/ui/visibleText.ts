// Names that come from outside (file paths, branch names, terminal titles)
// can hold characters that break the line or reorder the text around them.
// Shown through Vue's text interpolation they are inert as markup, but not as
// layout: a right-to-left override (U+202E) can make "evil<RLO>txt.exe" read
// as "evilexe.txt". These characters are shown as escapes instead.

/**
 * C0 controls, DEL, C1 controls, the Unicode line and paragraph separators,
 * and the bidirectional marks, embeddings, overrides and isolates.
 */
const HIDDEN = new RegExp('[\\u0000-\\u001f\\u007f-\\u009f\\u061c\\u200e\\u200f\\u2028-\\u202e\\u2066-\\u2069]', 'gu')

/** The text with every invisible or reordering character written as `\u{…}`. */
export function visibleText(s: string): string {
  return s.replace(HIDDEN, (c) => `\\u{${c.codePointAt(0)!.toString(16).toUpperCase()}}`)
}
