import { expect, test } from 'vitest'
import type { Column } from '../../api/types'
import { renderMarkdown, type TicketLookup } from './markdown'

const known: Record<number, { title: string; column: Column }> = { 12: { title: 'Fix the "login" <page>', column: 'review' } }
const lookup: TicketLookup = (n) => known[n]

function dom(text: string): HTMLElement {
  const el = document.createElement('div')
  el.innerHTML = renderMarkdown(text, lookup)
  return el
}

test('markdown: raw HTML is shown as text; javascript: links are not links; #12 becomes a link with title and column', () => {
  const el = dom('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>\n\n[click](javascript:alert(1)) and see #12.')
  expect(el.querySelector('script')).toBeNull()
  expect(el.querySelector('img')).toBeNull()
  expect(el.textContent).toContain('<script>alert(1)</script>')
  expect(el.textContent).toContain('[click](javascript:alert(1))')
  const links = [...el.querySelectorAll('a')]
  expect(links.map((a) => a.getAttribute('href'))).toEqual(['/12'])
  expect(links[0]!.textContent).toBe('#12')
  expect(links[0]!.getAttribute('title')).toBe('#12 Fix the "login" <page> (Review)')
  expect(links[0]!.hasAttribute('target')).toBe(false)
})

test('hostile link schemes never become links, in any spelling', () => {
  for (const url of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', ' javascript:alert(1)', 'vbscript:x', 'data:text/html;base64,PHNjcmlwdD4=', 'file:///etc/passwd', '/relative', '//evil.example/x']) {
    const el = dom(`[x](${url}) <${url}>`)
    expect(el.querySelectorAll('a'), url).toHaveLength(0)
  }
  const ref = dom('[x][r]\n\n[r]: javascript:alert(1)')
  expect(ref.querySelectorAll('a')).toHaveLength(0)
})

test('web links open in a new tab without access to omo; bare addresses are linked', () => {
  const el = dom('[docs](https://example.com/a) and https://example.org/b and <mailto:me@example.com>')
  const links = [...el.querySelectorAll('a')]
  expect(links.map((a) => a.getAttribute('href'))).toEqual(['https://example.com/a', 'https://example.org/b', 'mailto:me@example.com'])
  for (const a of links) {
    expect(a.getAttribute('target')).toBe('_blank')
    expect(a.getAttribute('rel')).toBe('noreferrer noopener')
  }
})

test('images are not loaded but shown as a link to their address', () => {
  const el = dom('![a diagram](https://example.com/d.png) ![](data:image/png;base64,AAAA) ![x](javascript:alert(1))')
  expect(el.querySelector('img')).toBeNull()
  const links = [...el.querySelectorAll('a')]
  expect(links).toHaveLength(1)
  expect(links[0]!.getAttribute('href')).toBe('https://example.com/d.png')
  expect(links[0]!.textContent).toBe('Image: a diagram')
  expect(links[0]!.getAttribute('rel')).toBe('noreferrer noopener')
})

test('ticket references: not inside code, links or words; unknown tickets still link', () => {
  const el = dom('`#12` and\n\n```\n#12\n```\n\n[see #12](https://example.com) a#12 #7, (#12) issue#3 #0')
  const refs = [...el.querySelectorAll('a[data-ticket-ref]')].map((a) => [a.textContent, a.getAttribute('title')])
  expect(refs).toEqual([
    ['#7', '#7'],
    ['#12', '#12 Fix the "login" <page> (Review)'],
  ])
  expect(el.querySelector('code')!.textContent).toBe('#12')
})

test('headings start below the panel headings and keep their order', () => {
  const el = dom("## Goal\n\ntext\n\n### Detail\n\n## Don't")
  expect([...el.querySelectorAll('h1,h2,h3,h4,h5,h6')].map((h) => h.tagName)).toEqual(['H4', 'H5', 'H4'])
  expect(dom('# Top\n\n###### Deep').querySelector('h6')).not.toBeNull()
})
