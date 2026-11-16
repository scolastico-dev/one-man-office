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

test('nothing calls the browser scrollIntoView, which can move the whole page: rows are shown with revealInBox', () => {
  const found = Object.entries(sources)
    .filter(([file, text]) => file !== '/src/components/ui/scrollBox.ts' && /\.scrollIntoView(IfNeeded)?\b/.test(text))
    .map(([file]) => file)
  expect(found).toEqual([])
})

/** Modal dialogs are drawn above the page (the top layer) and scroll themselves. */
const DIALOGS = new Set(['/src/components/ui/UiDialog.vue', '/src/features/ticket/TicketPanel.vue'])

test('every frame as tall as the window is positioned and clips: nothing inside, not even a screen-reader-only label, makes the document scroll', () => {
  const frames: string[] = []
  for (const [file, text] of Object.entries(sources)) {
    if (!file.endsWith('.vue') || DIALOGS.has(file)) continue
    const style = /<style[^>]*>([\s\S]*?)<\/style>/.exec(text)?.[1] ?? ''
    for (const m of style.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
      const body = m[2]!
      if (!/(^|[\s;])height:\s*100(d|s|l)?vh/.test(body)) continue
      frames.push(file)
      const where = `${file} ${m[1]!.trim()}`
      expect(body, where).toMatch(/position:\s*relative/)
      expect(body, where).toMatch(/overflow:\s*clip/)
    }
  }
  // The board's frame and a terminal's own window.
  expect(frames).toEqual(expect.arrayContaining(['/src/layout/AppShell.vue', '/src/features/terminal/TerminalWindow.vue']))
})

test('no scroll box swallows the other axis: overscroll is contained per axis, never with the shorthand, never sideways', () => {
  const found: string[] = []
  for (const [file, text] of Object.entries(sources)) {
    if (file === '/src/scroll.guard.test.ts') continue
    for (const m of text.matchAll(/overscroll-behavior(-[a-z]+)?\s*:\s*([a-z]+)/g)) {
      const axis = m[1] ?? ''
      const value = m[2]!
      // A box that scrolls only up and down must let a sideways wheel reach the board around it.
      if (axis === '' || ((axis === '-x' || axis === '-inline') && value !== 'auto')) found.push(`${file}: ${m[0]}`)
    }
  }
  expect(found).toEqual([])
})

test('nothing listens to the wheel: scrolling is left to the browser (a listener would break sideways wheels or the drag edge scroll)', () => {
  const found = Object.entries(sources)
    .filter(([, text]) => /@wheel\b|['"](mouse)?wheel['"]|\bonwheel\b/.test(text))
    .map(([file]) => file)
  expect(found).toEqual([])
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
