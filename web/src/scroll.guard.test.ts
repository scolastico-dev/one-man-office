import { expect, test } from 'vitest'

// The board's frame never scrolls as a document; the panes inside do.
// Every source file of the app except tests, as text.
const sources = import.meta.glob(['/src/**/*.{vue,ts,css}', '!/src/**/*.test.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

test('the guard sees the files it scans', () => {
  expect(Object.keys(sources).filter((f) => f.endsWith('.vue')).length).toBeGreaterThan(50)
  expect(sources['/src/layout/AppShell.vue']).toBeDefined()
})

test.each(Object.entries(sources))('nothing calls the browser scrollIntoView, which can move the whole page: rows are shown with revealInBox: %s', (file, text) => {
  if (file === '/src/components/ui/scrollBox.ts') return
  expect(text, file).not.toMatch(/\.scrollIntoView(IfNeeded)?\b/)
})

/** Modal dialogs are drawn above the page (the top layer) and scroll themselves. */
const DIALOGS = new Set(['/src/components/ui/UiDialog.vue', '/src/features/ticket/TicketPanel.vue'])

const frames = Object.entries(sources).flatMap(([file, text]) => {
  if (!file.endsWith('.vue') || DIALOGS.has(file)) return []
  const style = /<style[^>]*>([\s\S]*?)<\/style>/.exec(text)?.[1] ?? ''
  return [...style.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter((m) => /(^|[\s;])height:\s*100(d|s|l)?vh/.test(m[2]!))
    .map((m) => ({ file, where: `${file} ${m[1]!.trim()}`, body: m[2]! }))
})

test.each(frames)('every frame as tall as the window is positioned and clips: $where', ({ where, body }) => {
  expect(body, where).toMatch(/position:\s*relative/)
  expect(body, where).toMatch(/overflow:\s*clip/)
})

test('the frame guard sees the board and terminal window', () => {
  expect(frames.map((f) => f.file)).toEqual(expect.arrayContaining(['/src/layout/AppShell.vue', '/src/features/terminal/TerminalWindow.vue']))
})

test.each(Object.entries(sources))('no scroll box swallows the other axis: overscroll is contained per axis, never with the shorthand, never sideways: %s', (file, text) => {
  const found: string[] = []
  if (file === '/src/scroll.guard.test.ts') return
  for (const m of text.matchAll(/overscroll-behavior(-[a-z]+)?\s*:\s*([a-z]+)/g)) {
    const axis = m[1] ?? ''
    const value = m[2]!
    // A box that scrolls only up and down must let a sideways wheel reach the board around it.
    if (axis === '' || ((axis === '-x' || axis === '-inline') && value !== 'auto')) found.push(`${file}: ${m[0]}`)
  }
  expect(found).toEqual([])
})

test.each(Object.entries(sources))('nothing listens to the wheel: scrolling is left to the browser (a listener would break sideways wheels or the drag edge scroll): %s', (file, text) => {
  expect(text, file).not.toMatch(/@wheel\b|['"](mouse)?wheel['"]|\bonwheel\b/)
})

/** The declarations of the first rule of a file's styles with exactly this selector. */
function rule(file: string, selector: string): string {
  const text = sources[file]
  if (text === undefined) throw new Error(`${file} is not scanned`)
  const style = (/<style[^>]*>([\s\S]*?)<\/style>/.exec(text)?.[1] ?? '').replace(/\/\*[\s\S]*?\*\//g, '')
  for (const m of style.matchAll(/([^{}]+)\{([^{}]*)\}/g)) if (m[1]!.trim() === selector) return m[2]!
  throw new Error(`${file} has no rule ${selector}`)
}

test('the settings frame is the one scroll box of the settings area: the wheel scrolls the page over the list, the margins and the page alike', () => {
  // The frame scrolls; the section list sticks in it and the page column does not scroll apart from it.
  for (const [file, frame, side, page] of [
    ['/src/features/settings/SettingsView.vue', '.settings-view', '.settings-view__side', null],
    ['/src/sample/SampleSettings.vue', '.settings', '.settings__side', '.settings__page'],
  ] as const) {
    expect(rule(file, frame), `${file} ${frame}`).toMatch(/overflow-y:\s*auto/)
    expect(rule(file, side), `${file} ${side}`).toMatch(/position:\s*sticky/)
    if (page !== null) expect(rule(file, page), `${file} ${page}`).not.toMatch(/overflow/)
  }
  expect(rule('/src/features/settings/SettingsPage.vue', '.settings-page')).not.toMatch(/overflow/)
})

test('a scroll box a wheel can land on inside the board, the ticket panel, the image lightbox and the searched pickers is a composited scroller, so the compositor finds it without asking the main thread (which can drop the wheel)', () => {
  for (const [file, selector] of [
    ['/src/features/board/BoardColumn.vue', '.column__body'],
    ['/src/features/ticket/TicketPanel.vue', '.tpanel__scroll'],
    ['/src/features/images/ImageLightbox.vue', '.lightbox'],
    ['/src/components/ui/SearchPicker.vue', '.pick__list'],
  ] as const) {
    expect(rule(file, selector), `${file} ${selector}`).toMatch(/will-change:\s*scroll-position/)
  }
})

test('a code block of a description, which scrolls sideways, is a composited layer, so the compositor finds it under a wheel itself', () => {
  expect(rule('/src/features/ticket/DescriptionView.vue', '.md :deep(pre)')).toMatch(/will-change:\s*transform/)
})

// Every rule that makes a scroll box (overflow auto or scroll) either composites it (`will-change`), or is named here with the audit that
// found its wheel handled by the compositor alone: `wheel-states.spec.ts`, `wheel-popups.spec.ts` and the audit tests of `page-scroll.spec.ts`
// (see e2e/support/wheelAudit.ts). A new scroll box fails this test until it is audited in a populated state, and then composited or listed.
const AUDITED_CLEAN: Record<string, string> = {
  '/src/components/ui/UiDialog.vue .dialog': 'page-scroll (lightbox, panel), wheel-popups (history, folder picker), wheel-states (import, batch removal)',
  '/src/features/board/BoardView.vue .board': 'page-scroll (the board)',
  '/src/features/board/BoardView.vue .board__empty': 'page-scroll (the board)',
  '/src/features/diff/DiffBrowser.vue .dbrowse__main': 'page-scroll (diff)',
  '/src/features/diff/DiffBrowser.vue .dbrowse__tree': 'page-scroll (diff)',
  '/src/features/diff/DiffFileView.vue .dfile__body': 'page-scroll (diff, panel diff), board-wheel (long lines)',
  '/src/features/notifications/NotificationList.vue .bell-list': 'wheel-popups (the bell list)',
  '/src/features/settings/SettingsView.vue .settings-view': 'page-scroll (settings sections); the Environment page and pages with a focused text area ask the main thread because of their text fields, see settings-wheel',
  '/src/features/settings/SettingsView.vue .settings-view__side': 'page-scroll (settings sections)',
  '/src/features/settings/projects/FolderPickerDialog.vue .picker__list': 'wheel-popups (the folder picker)',
  '/src/features/settings/projects/ProjectList.vue .projects__gone--scroll': 'wheel-states (the batch removal list, overflowing)',
  '/src/features/sidebar/SidebarRoot.vue .side__tabs :deep([role=\'tabpanel\'])': 'wheel-popups (the sidebar tabs)',
  '/src/features/ticket/DescriptionHistoryDialog.vue .hist__list': 'wheel-popups (the description history)',
  '/src/features/ticket/DescriptionHistoryDialog.vue .hist__preview': 'wheel-popups (the description history)',
  '/src/features/ticket/TicketPanel.vue .tpanel__head': 'page-scroll (the panel)',
  '/src/features/ticket/page/TicketPage.vue .tpage': 'page-scroll (the ticket page)',
  '/src/features/ticket/page/TicketPage.vue .tpage__body:has([data-tab-content=\'overview\'])': 'page-scroll (the ticket page), wheel-states (markdown)',
  '/src/features/ticket/page/TicketPage.vue .tpage__side': 'page-scroll (the ticket page)',
  '/src/features/ticket/tabs/TicketTabs.vue .ptabs--page :deep(.tabs__list)': 'page-scroll (the ticket page)',
  '/src/features/ticket/tabs/TicketTabs.vue .ptabs--page :deep(.tabs__panel)': 'page-scroll (the ticket page)',
  // The scroller of a text field is the browser's own (a text area's inner box): no style composites it.
  '/src/features/settings/plugins/PluginConfigEditor.vue .plugin-config__code :deep(textarea)': 'a text area',
  '/src/features/settings/usage/ScriptEditor.vue .script-editor__code :deep(textarea)': 'a text area',
  '/src/features/ticket/DescriptionEditor.vue .editor__source': 'a text area',
  '/src/features/terminal/TerminalDraft.vue .td__field': 'a text area',
  // These three carry the class omo-logtext, which composites them (checked below).
  '/src/features/settings/mcp/McpServerLog.vue .mcp-log__text': 'omo-logtext',
  '/src/features/settings/plugins/PluginLog.vue .plugin-log__text': 'omo-logtext',
  '/src/features/terminal/LogView.vue .log__text': 'omo-logtext',
}

const scrollRules = Object.entries(sources).flatMap(([file, text]) => {
  if (file.startsWith('/src/sample/') || file === '/src/scroll.guard.test.ts') return []
  const style = file.endsWith('.vue') ? (/<style[^>]*>([\s\S]*?)<\/style>/.exec(text)?.[1] ?? '') : text
  return [...style.replace(/\/\*[\s\S]*?\*\//g, '').matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter((m) => /(^|[\s;])overflow(-[xy])?:\s*(auto|scroll)/.test(m[2]!))
    .map((m) => ({ key: `${file} ${m[1]!.trim().replace(/\s+/g, ' ')}`, body: m[2]! }))
})

test.each(scrollRules)('every scroll box rule composites its box or is listed with the audit that cleared it: $key', ({ key, body }) => {
  expect(/will-change:\s*(scroll-position|transform)/.test(body) || AUDITED_CLEAN[key] !== undefined, key).toBe(true)
})

test('no scroll audit entry outlives its rule', () => {
  const seen = new Set(scrollRules.map((r) => r.key))
  expect(Object.keys(AUDITED_CLEAN).filter((k) => !seen.has(k))).toEqual([])
})

test('the log boxes that rely on the class omo-logtext use it, and it composites them', () => {
  for (const file of ['/src/features/settings/mcp/McpServerLog.vue', '/src/features/settings/plugins/PluginLog.vue', '/src/features/terminal/LogView.vue']) {
    expect(sources[file], file).toMatch(/class="omo-logtext /)
  }
  expect(sources['/src/styles/main.css']).toMatch(/\.omo-logtext\s*\{[^}]*will-change:\s*transform/)
})
