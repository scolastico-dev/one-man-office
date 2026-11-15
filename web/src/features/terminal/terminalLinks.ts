import type { ILinkHandler } from '@xterm/xterm'

/** Only web addresses open from terminal output; never `javascript:`, `data:`, `file:` and the like. */
export function isOpenableLink(url: string): boolean {
  try {
    const u = new URL(url)
    return u.protocol === 'http:' || u.protocol === 'https:'
  } catch {
    return false
  }
}

/**
 * Handler for OSC 8 hyperlinks written by the program in the terminal. A link
 * opens only on Ctrl/Cmd+click and only when it is http or https; the new
 * page gets no opener and no referrer.
 */
export const linkHandler: ILinkHandler = {
  allowNonHttpProtocols: false,
  activate(event, text) {
    if (!(event.ctrlKey || event.metaKey)) return
    if (!isOpenableLink(text)) return
    window.open(text, '_blank', 'noopener,noreferrer')
  },
}
