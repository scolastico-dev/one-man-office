import { describe, expect, test } from 'vitest'
import type { Column } from '../../api/types'
import { hasHidden } from '../../components/ui/visibleText'
import { imageRefs, renderMarkdown, type RenderOptions, type TicketLookup } from './markdown'

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
  expect(links.map((a) => a.getAttribute('href'))).toEqual(['/ticket/12'])
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

test('markdown: mentions are highlighted outside code, with the agent name; raw HTML stays text', () => {
  const render = (text: string, mentions: Record<string, string>) => {
    const el = document.createElement('div')
    el.innerHTML = renderMarkdown(text, lookup, { mentions })
    return el
  }
  const el = render('@pm please look, `@pm` and email a@pm.io, @developer.\n\n```\n@pm\n```', { pm: 'Ada', developer: '' })
  const spans = [...el.querySelectorAll('span.mention')]
  expect(spans.map((s) => s.textContent)).toEqual(['@pm', '@developer'])
  expect(spans[0]!.getAttribute('data-role')).toBe('pm')
  expect(el.querySelectorAll('code span')).toHaveLength(0)
  expect(el.textContent).toContain('@pm (Ada) please')
  // Only the roles the server named are highlighted.
  expect(render('@reviewer', { pm: '' }).querySelector('.mention')).toBeNull()
  expect(render('@pm', {}).querySelector('.mention')).toBeNull()
  expect(render('@user hi', { user: '' }).querySelector('.mention--you')).not.toBeNull()
})

test('markdown: @reviewer:<project id> is one mention, with the project in the highlight', () => {
  const render = (text: string, mentions: Record<string, string>) => {
    const el = document.createElement('div')
    el.innerHTML = renderMarkdown(text, lookup, { mentions })
    return el
  }
  const el = render('@reviewer:web-app. is it fine? @reviewer:api and @reviewer:Web.', { reviewer: 'keen-owl' })
  const spans = [...el.querySelectorAll('span.mention')]
  expect(spans.map((s) => s.textContent)).toEqual(['@reviewer:web-app', '@reviewer:api', '@reviewer'])
  expect(spans.map((s) => s.getAttribute('data-project'))).toEqual(['web-app', 'api', null])
  // The agent is named once, after the first project; the sentence's full stop stays outside.
  expect(el.textContent?.trim()).toBe('@reviewer:web-app (keen-owl). is it fine? @reviewer:api and @reviewer:Web.')
  // A plain @reviewer keeps the agent's name; a reviewer in code is no mention.
  expect(render('@reviewer please', { reviewer: 'keen-owl' }).textContent?.trim()).toBe('@reviewer (keen-owl) please')
  expect(render('`@reviewer:web`', { reviewer: 'keen-owl' }).querySelector('.mention')).toBeNull()
})

test('markdown: a name with markup stays text and raw HTML in a mentioning comment is not parsed', () => {
  const el = document.createElement('div')
  el.innerHTML = renderMarkdown('@pm <img src=x onerror=alert(1)> <b>x</b>', lookup, { mentions: { pm: '<b>Ada</b><script>1</script>' } })
  expect(el.querySelector('b')).toBeNull()
  expect(el.querySelector('img')).toBeNull()
  expect(el.querySelector('script')).toBeNull()
  expect(el.textContent).toContain('@pm (<b>Ada</b><script>1</script>)')
})

describe('hidden characters', () => {
  const RLO = '‮'
  const ISO = '⁦'
  const ESC = '\u001b'
  const BEL = '\u0007'
  const ZWSP = '​'
  const TAG = String.fromCodePoint(0xe0041)
  const all = [RLO, ISO, ESC, BEL, ZWSP, TAG]
  const shown = ['\\u{202E}', '\\u{2066}', '\\u{1B}', '\\u{7}', '\\u{200B}', '\\u{E0041}']
  const payload = `a${all.join('b')}c`
  const escaped = `a${shown.join('b')}c`

  function hiddenIn(s: string): string[] {
    return [...s].filter((c) => hasHidden(c) && c !== '\n' && c !== '\t').map((c) => c.codePointAt(0)!.toString(16))
  }

  function expectClean(text: string, options: RenderOptions = {}) {
    const html = renderMarkdown(text, lookup, options)
    expect(hiddenIn(html), html).toEqual([])
    return html
  }

  test('in text, emphasis, headings, lists and quotes', () => {
    for (const text of [payload, `a&#x202E;b&#8203;c ${payload}`, `*${payload}*`, `**${payload}**`, `# ${payload}`, `- ${payload}`, `> ${payload}`, `| h |\n|---|\n| ${payload} |`]) {
      const el = document.createElement('div')
      el.innerHTML = expectClean(text)
      expect(el.textContent).toContain(escaped)
    }
  })

  test('in inline code, fenced and indented code, with line breaks and tabs kept', () => {
    for (const text of [`\`${payload}\``, `\`\`\`sh\necho ${payload}\n\tnext\n\`\`\``, `    echo ${payload}\n    \tnext`, `~~~\n${payload}\n~~~`]) {
      const el = document.createElement('div')
      el.innerHTML = expectClean(text)
      const code = el.querySelector('code')!
      expect(code.textContent).toContain(escaped)
    }
    const el = document.createElement('div')
    el.innerHTML = expectClean(`\`\`\`\nprintf 'x${ESC}[201~'\n\trm -rf ~\n\`\`\``)
    expect(el.querySelector('code')!.textContent).toBe("printf 'x\\u{1B}[201~'\n\trm -rf ~\n")
  })

  test('in a fence info string', () => {
    expectClean(`\`\`\`sh${RLO}x\ncode\n\`\`\``)
  })

  test('in link text, link titles, autolinks, image alt text and titles', () => {
    for (const text of [
      `[${payload}](https://example.test/)`,
      `[x](https://example.test/ "${payload}")`,
      `<https://example.test/${payload}>`,
      `https://example.test/${RLO}x`,
      `![${payload}](https://example.test/i.png)`,
      `![x](https://example.test/i.png "${payload}")`,
      `![x](https://example.test/${RLO}.png)`,
    ]) {
      expectClean(text)
    }
    const el = document.createElement('div')
    el.innerHTML = expectClean(`[${payload}](https://example.test/ "t${RLO}")`)
    const a = el.querySelector('a')!
    expect(a.textContent).toBe(escaped)
    expect(a.getAttribute('title')).toBe('t\\u{202E}')
  })

  test('in ticket titles of #n links and in mention names', () => {
    const hostile: TicketLookup = () => ({ title: `evil${RLO}txt`, column: 'review' })
    const el = document.createElement('div')
    el.innerHTML = renderMarkdown(`see #5 @developer`, hostile, { mentions: { developer: `Ann${RLO}${ESC}` } })
    expect(hiddenIn(el.innerHTML)).toEqual([])
    expect(el.querySelector('a')!.getAttribute('title')).toBe('#5 evil\\u{202E}txt (Review)')
    expect(el.textContent).toContain('(Ann\\u{202E}\\u{1B})')
  })

  test('mentions and ticket references still work next to escaped characters', () => {
    const el = document.createElement('div')
    el.innerHTML = renderMarkdown(`${RLO} #12 @developer`, lookup, { mentions: { developer: '' } })
    expect(el.querySelector('a[data-ticket-ref="12"]')).not.toBeNull()
    expect(el.querySelector('.mention')!.textContent).toBe('@developer')
  })
})

describe('uploaded images (omo-image:)', () => {
  const id = '0f8fad5b-d9cb-469f-a165-70867728950e'
  const render = (text: string, images?: RenderOptions['images']) => {
    const el = document.createElement('div')
    el.innerHTML = renderMarkdown(text, lookup, { images })
    return el
  }

  test('a stored image shows its picture through the object URL, the alt text as its name', () => {
    const el = render(`See ![screen <b>shot</b>.png](omo-image:${id})`, () => ({ state: 'shown', url: 'blob:http://omo.localhost/1' }))
    const img = el.querySelector('img')!
    expect(img.getAttribute('src')).toBe('blob:http://omo.localhost/1')
    expect(img.getAttribute('alt')).toBe('screen <b>shot</b>.png')
    expect(el.querySelector('b')).toBeNull()
    const button = el.querySelector('button[data-omo-image]')!
    expect(button.getAttribute('data-omo-image')).toBe(id)
    expect(button.getAttribute('type')).toBe('button')
  })

  test('only an object URL becomes a picture', () => {
    for (const url of ['javascript:alert(1)', 'https://evil.example/x.png', `/api/images/${id}/content`, 'data:image/png;base64,AAAA']) {
      const el = render(`![x](omo-image:${id})`, () => ({ state: 'shown', url }))
      expect(el.querySelector('img'), url).toBeNull()
      expect(el.textContent!.trim()).toBe('x (image could not be loaded)')
    }
  })

  test('while loading, expired or missing the alt text shows with a note', () => {
    for (const [state, note] of [
      ['loading', 'loading image…'],
      ['expired', 'image expired'],
      ['missing', 'image missing'],
      ['failed', 'image could not be loaded'],
    ] as const) {
      const el = render(`![shot.png](omo-image:${id})`, () => ({ state }))
      expect(el.textContent!.trim()).toBe(`shot.png (${note})`)
      expect(el.querySelector('img')).toBeNull()
      expect(el.querySelector('[data-omo-image]')!.getAttribute('data-omo-image-state')).toBe(state)
    }
    expect(render(`![shot.png](omo-image:${id})`).textContent!.trim()).toBe('shot.png (image)')
  })

  test('hidden characters in the alt text are shown, not used', () => {
    const el = render(`![a‮b.png](omo-image:${id})`, () => ({ state: 'shown', url: 'blob:x' }))
    expect(el.querySelector('img')!.getAttribute('alt')).toBe('a\\u{202E}b.png')
  })

  test('the reference resolves nothing else: other schemes, a bad id, or a link to it', () => {
    const el = render(
      `![a](omo-image:not-an-id) ![b](omo-image:${id.toUpperCase()}) [c](omo-image:${id}) ![d](https://x.example/d.png)`,
      () => ({ state: 'shown', url: 'blob:x' }),
    )
    expect(el.querySelectorAll('img')).toHaveLength(0)
    expect(el.querySelectorAll('a[href^="omo-image"]')).toHaveLength(0)
    expect(el.textContent).toContain('c')
    expect([...el.querySelectorAll('a')].map((a) => a.getAttribute('href'))).toEqual(['https://x.example/d.png'])
  })

  test('imageRefs lists each referenced id once, escaped alt text included', () => {
    const other = '7c9e6679-7425-40de-944b-e07fc1f90ae7'
    expect(imageRefs(`![a](omo-image:${id}) ![b\\]c](omo-image:${other}) ![a](omo-image:${id}) [x](omo-image:${id}x)`)).toEqual([id, other])
  })
})
