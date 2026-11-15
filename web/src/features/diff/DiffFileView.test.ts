import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import type { DiffFile } from '../../api/types'
import DiffFileView from './DiffFileView.vue'
import { LINE_LIMIT } from './parsePatch'

function file(o: Partial<DiffFile> = {}): DiffFile {
  return { path: 'src/a.ts', old_path: '', status: 'modified', binary: false, additions: 1, deletions: 1, patch: null, truncated: false, ...o }
}

function view(f: DiffFile, extra: Record<string, unknown> = {}) {
  return mount(DiffFileView, { props: { file: f, domId: 'f1', open: true, ...extra } })
}

const RLO = '‮'

describe('DiffFileView', () => {
  test('signs and hidden words for added and removed lines', () => {
    const w = view(file({ patch: '@@ -1,2 +1,2 @@\n same\n-old\n+new\n' }))
    const add = w.get('[data-row="add"]')
    const del = w.get('[data-row="del"]')
    const ctx = w.get('[data-row="ctx"]')
    expect(add.get('[aria-hidden="true"]').text()).toBe('+')
    expect(add.get('.sr-only').text()).toBe('added')
    expect(del.get('[aria-hidden="true"]').text()).toBe('−')
    expect(del.get('.sr-only').text()).toBe('removed')
    expect(ctx.find('.sr-only').exists()).toBe(false)
    expect(add.get('[data-text]').text()).toBe('new')
    // Line numbers: old for removed, new for added, both for context.
    expect(del.findAll('[data-no]').map((x) => x.text())).toEqual(['2', ''])
    expect(add.findAll('[data-no]').map((x) => x.text())).toEqual(['', '2'])
    expect(ctx.findAll('[data-no]').map((x) => x.text())).toEqual(['1', '1'])
    expect(w.get('[data-hunk]').text()).toBe('@@ -1,2 +1,2 @@')
    expect(w.get('[role="region"]').attributes('aria-label')).toBe('Changes in src/a.ts')
    const u = mount(DiffFileView, { props: { file: file({ patch: '@@ -1 +1 @@\n+x\n' }), domId: 'f2', open: true, uncommitted: true } })
    expect(u.get('[role="region"]').attributes('aria-label')).toBe('Uncommitted changes in src/a.ts')
  })

  test('status in words and the old name of a renamed file', () => {
    const w = view(file({ status: 'renamed', path: 'b.ts', old_path: 'a.ts', patch: null, additions: 0, deletions: 0 }))
    expect(w.get('[data-status]').text()).toContain('renamed')
    expect(w.text()).toContain('renamed from a.ts')
  })

  test('the toggle says whether it is open and asks to change it', async () => {
    const w = view(file({ patch: '@@ -1 +1 @@\n+x\n' }), { open: false })
    const toggle = w.get('button[aria-expanded]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(w.find('[data-row]').exists()).toBe(false)
    await toggle.trigger('click')
    expect(w.emitted('toggle')).toEqual([[true]])
  })

  test('binary, untracked, too large and cut files say so', async () => {
    expect(view(file({ binary: true })).get('[data-note]').text()).toMatch(/Binary file/)
    expect(view(file({ status: 'untracked' })).get('[data-note]').text()).toMatch(/Not committed yet and not tracked/)
    const big = view(file({ patch: null, truncated: true }))
    expect(big.get('[data-note]').text()).toMatch(/Too large to show here/)
    await big.get('button[data-more]').trigger('click')
    expect(big.emitted('more')).toHaveLength(1)
    const cut = view(file({ patch: '@@ -1,9 +1,9 @@\n a\n', truncated: true }))
    expect(cut.get('[data-note]').text()).toMatch(/Shown in part/)
    expect(cut.get('button[data-more]').text()).toBe('Show more')
    // Once the whole file the server allows is shown, there is nothing more to ask for.
    const done = view(file({ patch: '@@ -1,9 +1,9 @@\n a\n', truncated: true }), { loaded: true })
    expect(done.get('[data-note]').text()).toMatch(/Shown in part/)
    expect(done.find('button[data-more]').exists()).toBe(false)
    const failed = view(file({ patch: null, truncated: true }), { error: 'The file is not part of this diff.' })
    expect(failed.get('[role="alert"]').text()).toBe('The file is not part of this diff.')
  })

  test('long lines are cut with a note', () => {
    const w = view(file({ patch: `@@ -0,0 +1 @@\n+${'x'.repeat(LINE_LIMIT + 10)}\n` }))
    const row = w.get('[data-row="add"]')
    expect(row.get('[data-text]').text()).toHaveLength(LINE_LIMIT)
    expect(row.get('[data-cut]').text()).toContain('10 more characters')
  })

  test('control characters, bidi overrides and HTML in a name and in a patch line are shown as text', () => {
    const name = `evil${RLO}gpj.exe<img src=x onerror="alert(1)">\n\u0007`
    const w = view(
      file({
        path: name,
        old_path: `old${RLO}<b>x</b>`,
        status: 'renamed',
        patch: `@@ -1 +1 @@ <script>alert(1)</script>${RLO}\n-<img src=x onerror=alert(2)>\n+ok${RLO}\u001b[2J\u0000\r\n`,
      }),
    )
    expect(w.find('img').exists()).toBe(false)
    expect(w.find('script').exists()).toBe(false)
    expect(w.find('b').exists()).toBe(false)
    expect(w.text()).not.toContain(RLO)
    expect(w.text()).not.toMatch(/[\u0000-\u0008\u000b-\u001f]/)
    expect(w.get('[data-path]').text()).toBe('evil\\u{202E}gpj.exe<img src=x onerror="alert(1)">\\u{A}\\u{7}')
    expect(w.text()).toContain('renamed from old\\u{202E}<b>x</b>')
    expect(w.get('[data-hunk]').text()).toBe('@@ -1 +1 @@ <script>alert(1)</script>\\u{202E}')
    expect(w.get('[data-row="del"] [data-text]').text()).toBe('<img src=x onerror=alert(2)>')
    expect(w.get('[data-row="add"] [data-text]').text()).toBe('ok\\u{202E}\\u{1B}[2J\\u{0}\\u{D}')
    expect(w.get('[role="region"]').attributes('aria-label')).toBe(`Changes in evil\\u{202E}gpj.exe<img src=x onerror="alert(1)">\\u{A}\\u{7}`)
    // Only the view's own elements exist: nothing in the names or lines became markup.
    const tags = new Set([...w.element.querySelectorAll('*')].map((e) => e.tagName.toLowerCase()))
    for (const t of tags) expect(['li', 'button', 'span', 'div', 'p']).toContain(t)
  })
})

describe('the diff feature', () => {
  test('no v-html anywhere in features/diff', () => {
    const sources = import.meta.glob(['./*.{vue,ts}', '../ticket/tabs/DiffTab.vue', '!./*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<
      string,
      string
    >
    expect(Object.keys(sources).length).toBeGreaterThanOrEqual(6)
    for (const [name, text] of Object.entries(sources)) {
      expect(text, name).not.toMatch(/v-html|innerHTML|outerHTML|insertAdjacentHTML/)
    }
  })
})
