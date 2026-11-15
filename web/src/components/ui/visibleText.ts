// Names that come from outside (file paths, branch names, terminal titles)
// can hold characters that break the line, reorder the text around them or
// hide text. Shown through Vue's text interpolation they are inert as markup,
// but not as layout: a right-to-left override (U+202E) can make
// "evil<RLO>txt.exe" read as "evilexe.txt", and a zero width space or a tag
// character carries text nobody sees. These characters are shown as escapes
// instead. The set is the server's (internal/textsafe); both are tested
// against the shared list internal/textsafe/testdata/hidden.json.

/**
 * C0 controls, DEL, C1 controls, the soft hyphen, the Unicode line and
 * paragraph separators, the bidirectional marks, embeddings, overrides and
 * isolates, the zero width space, word joiner and invisible operators, the
 * zero width no-break space, the deprecated format controls, the interlinear
 * annotation characters and the tag characters. The joiners U+200C and
 * U+200D are kept: emoji and several scripts need them.
 */
const HIDDEN_CLASS =
  '[\\u0000-\\u001f\\u007f-\\u009f\\u00ad\\u061c\\u200b\\u200e\\u200f\\u2028-\\u202e\\u2060-\\u2064\\u2066-\\u206f\\ufeff\\ufff9-\\ufffb\\u{e0000}-\\u{e007f}]'
const HIDDEN = new RegExp(HIDDEN_CLASS, 'gu')
const ANY_HIDDEN = new RegExp(HIDDEN_CLASS, 'u')

const escape = (c: string) => `\\u{${c.codePointAt(0)!.toString(16).toUpperCase()}}`

/** The text with every invisible or reordering character written as `\u{…}`. */
export function visibleText(s: string): string {
  return s.replace(HIDDEN, escape)
}

/** Like `visibleText`, but line breaks (`\n`) and tabs stay as they are: for text shown as several lines. */
export function visibleLines(s: string): string {
  return s.replace(HIDDEN, (c) => (c === '\n' || c === '\t' ? c : escape(c)))
}

/** Whether the text holds a character `visibleText` escapes (a title may not). */
export function hasHidden(s: string): boolean {
  return ANY_HIDDEN.test(s)
}
