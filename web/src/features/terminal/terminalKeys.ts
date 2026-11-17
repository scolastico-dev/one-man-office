// Keys a terminal emulator cannot tell apart that agent CLIs (Claude Code,
// Codex, Gemini, agy) treat differently. xterm.js sends a plain CR for Enter,
// Shift+Enter and Ctrl+Enter, so a CLI would submit on all three, while it
// inserts a line break on Shift+Enter and Ctrl+Enter. The kitty keyboard
// protocol that would tell them apart is not available, so the line break is
// sent the way Alt+Enter is sent: ESC followed by CR. All three CLIs read it
// as "newline without submit" (Claude Code's own terminal setup binds
// Shift+Enter to it; Codex and Gemini list Alt+Enter as a newline key).
// Alt+Enter itself already arrives as ESC CR from xterm, and Shift+Tab as
// ESC [ Z; both are left alone.

/** ESC CR: Alt+Enter in a terminal without extended keys. */
export const LINE_BREAK = '\x1b\r'

/** The bytes a modified Enter sends instead of xterm's CR, or null when xterm's own are right. */
export function lineBreakKey(ev: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'altKey' | 'metaKey'>): string | null {
  if (ev.key !== 'Enter' || ev.altKey || ev.metaKey) return null
  return ev.shiftKey || ev.ctrlKey ? LINE_BREAK : null
}
