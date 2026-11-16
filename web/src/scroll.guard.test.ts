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
