// Turns a ticket description (Markdown written by people and agents) into
// HTML that is safe to put into the page:
// - raw HTML in the text is shown as text, never parsed;
// - a link must be http, https or mailto; anything else (javascript:, data:,
//   relative paths) stays plain text; links open in a new tab without
//   giving that tab access to omo;
// - images are never loaded: an image becomes a link to its address, so a
//   description cannot load anything from the network or track who reads it;
// - `#12` becomes a link to ticket #12, with its title and column as the
//   tooltip when the board knows it; never inside code or another link;
// - headings start at level 4, so they sit below the panel's own headings.
import markdownIt, { type MarkdownIt, type Token } from 'markdown-it'
import type { Column } from '../../api/types'
import { COLUMN_TITLES } from '../board/model'

export type TicketLookup = (n: number) => { title: string; column: Column } | undefined

const ALLOWED_LINK = /^(https?:|mailto:)/i
/** The highest heading level a description renders. */
const TOP_HEADING = 4
/** `#12` after the start of the text or a character that is not part of a word. */
const TICKET_REF = /(^|[^\p{L}\p{N}_&/#])#(\d{1,9})(?![\p{L}\p{N}_])/gu

function createRenderer(): MarkdownIt {
  const md = markdownIt('default', { html: false, linkify: true, typographer: false, breaks: false })
  md.validateLink = (url) => ALLOWED_LINK.test(url.trim())
  md.linkify.set({ fuzzyEmail: false })

  md.renderer.rules.link_open = (tokens, idx, options, _env, self) => {
    const token = tokens[idx]!
    if (token.attrGet('data-ticket-ref') === null) {
      token.attrSet('target', '_blank')
      token.attrSet('rel', 'noreferrer noopener')
    }
    return self.renderToken(tokens, idx, options)
  }

  // An image is shown as a link to its address; nothing is fetched.
  md.renderer.rules.image = (tokens, idx) => {
    const token = tokens[idx]!
    const src = String(token.attrGet('src') ?? '')
    const alt = token.children ? md.renderer.renderInlineAsText(token.children, md.options, {}) : token.content
    const label = `Image: ${alt === '' ? src : alt}`
    const esc = md.utils.escapeHtml
    return `<a href="${esc(src)}" target="_blank" rel="noreferrer noopener" class="md-image" title="${esc(src)}">${esc(label)}</a>`
  }

  md.core.ruler.push('omo_headings', (state) => {
    let top = 6
    for (const t of state.tokens) if (t.type === 'heading_open') top = Math.min(top, Number(t.tag.slice(1)))
    const shift = TOP_HEADING - top
    for (const t of state.tokens) {
      if (t.type !== 'heading_open' && t.type !== 'heading_close') continue
      t.tag = `h${Math.min(6, Number(t.tag.slice(1)) + shift)}`
    }
  })

  md.core.ruler.push('omo_ticket_refs', (state) => {
    const lookup = (state.env as { lookup?: TicketLookup }).lookup
    for (const block of state.tokens) {
      if (block.type !== 'inline' || !block.children) continue
      const out: Token[] = []
      let inLink = 0
      for (const t of block.children) {
        if (t.type === 'link_open') inLink++
        if (t.type === 'link_close') inLink--
        if (t.type !== 'text' || inLink > 0) {
          out.push(t)
          continue
        }
        out.push(...splitRefs(state.Token, t, lookup))
      }
      block.children = out
    }
  })
  return md
}

type TokenClass = new (type: string, tag: string, nesting: -1 | 0 | 1) => Token

function splitRefs(TokenCtor: TokenClass, t: Token, lookup: TicketLookup | undefined): Token[] {
  const text = t.content
  const out: Token[] = []
  let last = 0
  for (const m of text.matchAll(TICKET_REF)) {
    const lead = m[1] ?? ''
    const digits = m[2] ?? ''
    const start = (m.index ?? 0) + lead.length
    const n = Number(digits)
    if (!Number.isSafeInteger(n) || n <= 0) continue
    if (start > last) out.push(textToken(TokenCtor, text.slice(last, start), t.level))
    const known = lookup?.(n)
    const open = new TokenCtor('link_open', 'a', 1)
    open.attrs = [
      ['href', `/${n}`],
      ['data-ticket-ref', String(n)],
      ['title', known ? `#${n} ${known.title} (${COLUMN_TITLES[known.column]})` : `#${n}`],
    ]
    open.level = t.level
    out.push(open, textToken(TokenCtor, `#${n}`, t.level + 1))
    const close = new TokenCtor('link_close', 'a', -1)
    close.level = t.level
    out.push(close)
    last = start + 1 + digits.length
  }
  if (last === 0) return [t]
  if (last < text.length) out.push(textToken(TokenCtor, text.slice(last), t.level))
  return out
}

function textToken(TokenCtor: TokenClass, content: string, level: number): Token {
  const t = new TokenCtor('text', '', 0)
  t.content = content
  t.level = level
  return t
}

let renderer: MarkdownIt | null = null

/** The description as safe HTML; `lookup` names the tickets that `#n` refers to. */
export function renderMarkdown(text: string, lookup: TicketLookup): string {
  renderer ??= createRenderer()
  return renderer.render(text, { lookup })
}
