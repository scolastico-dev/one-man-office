import type { IBufferRange, ILinkHandler } from '@xterm/xterm'

/** Only web addresses open from terminal output; never `javascript:`, `data:`, `file:` and the like. */
export function isOpenableLink(url: string): boolean {
  try {
    const u = new URL(url)
    return u.protocol === 'http:' || u.protocol === 'https:'
  } catch {
    return false
  }
}

/** Opens a web address in a new page with no opener and no referrer; anything else is ignored. */
export function openLink(url: string): void {
  if (isOpenableLink(url)) window.open(url, '_blank', 'noopener,noreferrer')
}

export interface LinkDeps {
  /** The text the terminal shows for the link. */
  textOf(range: IBufferRange): string
  /** Asks the user before opening `url`, which the link shows as `shown`. */
  confirm(url: string, shown: string): void
}

/**
 * Handler for OSC 8 hyperlinks written by the program in the terminal. A link
 * opens only on Ctrl/Cmd+click and only when it is http or https. The program
 * chooses both the text and the address, so a link whose text is not its
 * address opens only after the user saw the address and confirmed it.
 */
export function createLinkHandler(deps: LinkDeps): ILinkHandler {
  return {
    allowNonHttpProtocols: false,
    activate(event, url, range) {
      if (!(event.ctrlKey || event.metaKey)) return
      if (!isOpenableLink(url)) return
      const shown = deps.textOf(range).trim()
      if (shown === url) openLink(url)
      else deps.confirm(url, shown)
    },
  }
}
