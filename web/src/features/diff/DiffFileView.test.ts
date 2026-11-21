import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import type { DiffFile } from '../../api/types'
import DiffFileView from './DiffFileView.vue'
import { LINE_LIMIT, parsePatch, ROW_STEP } from './parsePatch'

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

  test('a huge patch renders its first rows and more on request', async () => {
    const n = 6_000
    const patch = `@@ -0,0 +1,${n} @@\n` + Array.from({ length: n }, (_, i) => `+line ${i + 1}`).join('\n') + '\n'
    const w = view(file({ patch, additions: n, deletions: 0 }))
    // The hunk header counts as a row.
    expect(w.findAll('.drow')).toHaveLength(ROW_STEP)
    expect(w.get('button[data-more-rows]').text()).toBe('Show 1,001 more lines')
    expect(w.get('[data-rows-left]').text()).toContain('1,001 more lines')
    await w.get('button[data-more-rows]').trigger('click')
    expect(w.findAll('.drow')).toHaveLength(n + 1)
    expect(w.find('button[data-more-rows]').exists()).toBe(false)
    expect(w.findAll('[data-text]').at(-1)!.text()).toBe(`line ${n}`)
    // A new patch starts from the first rows again.
    await w.setProps({ file: file({ patch: patch.replace('line 1\n', 'line one\n') }) })
    expect(w.findAll('.drow')).toHaveLength(ROW_STEP)
  }, 20_000)

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
  const sources = import.meta.glob(['./*.{vue,ts}', '../ticket/tabs/DiffTab.vue', '!./*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>
  test('the diff HTML guard sees the feature sources', () => {
    expect(Object.keys(sources).length).toBeGreaterThanOrEqual(6)
  })
  test.each(Object.entries(sources))('no v-html anywhere in features/diff: %s', (name, text) => {
    expect(text, name).not.toMatch(/v-html|innerHTML|outerHTML|insertAdjacentHTML/)
  })
})

/** A file of `n` lines "line i", lines in `changed` changed, as git writes it with `c` lines of context. */
function patchOf(n: number, changed: number[], c: number): string {
  const groups: number[][] = []
  for (const x of changed) {
    const last = groups[groups.length - 1]
    if (last && x - last[last.length - 1]! - 1 <= 2 * c) last.push(x)
    else groups.push([x])
  }
  let out = ''
  for (const g of groups) {
    const from = Math.max(1, g[0]! - c)
    const to = Math.min(n, g[g.length - 1]! + c)
    out += `@@ -${from},${to - from + 1} +${from},${to - from + 1} @@ fn()\n`
    for (let i = from; i <= to; i++) out += g.includes(i) ? `-line ${i}\n+line ${i} changed\n` : ` line ${i}\n`
  }
  return out
}

describe('DiffFileView hidden lines', () => {
  const long = () => file({ path: 'src/f.ts', patch: patchOf(200, [50, 150], 3), additions: 2, deletions: 2 })

  test('expander rows above, between and below the changes, with names that say which lines of which file', async () => {
    const w = view(long())
    expect(w.findAll('[data-gap]').map((g) => g.attributes('data-gap'))).toEqual(['top', 'between', 'bottom'])
    const names = w.findAll('button[data-expand]').map((b) => b.attributes('aria-label'))
    expect(names).toEqual([
      'Show 20 hidden lines above line 47 of src/f.ts',
      'Show all 46 hidden lines above line 47 of src/f.ts',
      'Show 20 hidden lines below line 53 of src/f.ts',
      'Show 20 hidden lines above line 147 of src/f.ts',
      'Show all 93 hidden lines between lines 53 and 147 of src/f.ts',
      'Show 20 more lines below line 153 of src/f.ts',
      'Show the rest of the file below line 153 of src/f.ts',
    ])
    // The words on a button begin its name.
    for (const b of w.findAll('button[data-expand]')) expect(b.attributes('aria-label')!.startsWith(b.text())).toBe(true)
    // The hunk's own header stays on the expander row.
    expect(w.findAll('[data-gap] [data-hunk]').map((h) => h.text())).toEqual(['@@ -47,7 +47,7 @@ fn()', '@@ -147,7 +147,7 @@ fn()'])
    await w.findAll('button[data-expand]')[2]!.trigger('click')
    expect(w.emitted('expand')![0]![0]).toMatchObject({ kind: 'below', gap: { place: 'between', from: 54, count: 93 } })
    await w.get('button[data-whole-file]').trigger('click')
    expect(w.emitted('expand')![1]![0]).toEqual({ kind: 'all' })
    expect(w.get('button[data-whole-file]').attributes('aria-label')).toBe('Show the whole file src/f.ts')
  })

  test('revealed lines show as context with both numbers through the same escaping, and the gap shrinks', () => {
    const wide = patchOf(200, [50, 150], 23).replace(' line 60\n', ' line 60 ‮evil\n')
    const w = view(long(), {
      expansion: {
        basePatch: long().patch,
        reveal: { ranges: [{ from: 54, to: 73 }], all: false },
        wider: { parsed: parsePatch(wide), context: 23 },
        loading: false,
        error: '',
        errorAt: 0,
      },
    })
    const row = w.findAll('[data-row="ctx"]').find((r) => r.findAll('[data-no]')[1]!.text() === '60')!
    expect(row.findAll('[data-no]').map((n) => n.text())).toEqual(['60', '60'])
    expect(row.get('[data-text]').text()).toBe('line 60 \\u{202E}evil')
    expect(w.find('[data-gap-before="73"]').exists()).toBe(true)
    expect(w.findAll('[data-row="ctx"]').map((r) => r.findAll('[data-no]')[1]!.text())).toContain('73')
    expect(w.findAll('[data-row="ctx"]').map((r) => r.findAll('[data-no]')[1]!.text())).not.toContain('74')
  })

  test('a refusal shows in place; loading disables the expanders', () => {
    const base = { basePatch: long().patch!, reveal: { ranges: [], all: false }, wider: null }
    const w = view(long(), { expansion: { ...base, loading: false, error: 'Too large.', errorAt: 54 } })
    expect(w.get('[data-gap-before="53"] [data-expand-error]').text()).toBe('Too large.')
    expect(w.findAll('[data-expand-error]')).toHaveLength(1)
    const whole = view(long(), { expansion: { ...base, loading: false, error: 'Too large.', errorAt: 0 } })
    expect(whole.get('[data-expand-tools] [data-expand-error]').text()).toBe('Too large.')
    const busy = view(long(), { expansion: { ...base, loading: true, error: '', errorAt: 54 } })
    expect(busy.findAll('button[data-expand]').every((b) => b.attributes('disabled') !== undefined)).toBe(true)
    expect(busy.get('[data-gap-before="53"] [data-expand-loading]').text()).toBe('Loading…')
  })

  test('no expanders for new, deleted, binary or cut files', () => {
    for (const o of [
      { status: 'added' as const, patch: '@@ -0,0 +1,2 @@\n+a\n+b\n' },
      { status: 'deleted' as const, patch: '@@ -1,2 +0,0 @@\n-a\n-b\n' },
      { binary: true, patch: null },
      { truncated: true, patch: patchOf(200, [50], 3) },
    ]) {
      const w = view(file(o))
      expect(w.find('[data-gap]').exists()).toBe(false)
      expect(w.find('[data-whole-file]').exists()).toBe(false)
    }
  })

  test('the row cap counts expander rows and revealed lines too', () => {
    const n = 12_000
    const whole = patchOf(n, [6000], n)
    const f = file({ patch: patchOf(n, [6000], 3) })
    const w = view(f, { expansion: { basePatch: f.patch, reveal: { ranges: [], all: true }, wider: { parsed: parsePatch(whole), context: 'all' }, loading: false, error: '', errorAt: 0 } })
    expect(w.findAll('.drow')).toHaveLength(ROW_STEP)
    expect(w.get('button[data-more-rows]').text()).toBe('Show 5,000 more lines')
  }, 20_000)
})
