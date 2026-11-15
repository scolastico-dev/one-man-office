import { expect, type BrowserContext, type Page } from '@playwright/test'
import type { Omo } from './omo'

export type ColumnKey = 'planning' | 'ready' | 'in_progress' | 'review' | 'done'
export type ColumnTitle = 'Planning' | 'Ready For Dev' | 'In Progress' | 'Review' | 'Done'

const KEYS: Record<ColumnTitle, ColumnKey> = {
  Planning: 'planning',
  'Ready For Dev': 'ready',
  'In Progress': 'in_progress',
  Review: 'review',
  Done: 'done',
}
export const COLUMN_KEYS: readonly ColumnKey[] = ['planning', 'ready', 'in_progress', 'review', 'done']

/** The board's address for a test: the printed one (with token), or the plain one in basic mode. */
function entry(omo: Omo, omoAuth: 'token' | 'basic' | 'none'): string {
  return omoAuth === 'token' ? omo.accessUrl : omo.url('/')
}

/**
 * Opens the board and waits until it is shown and live. With `auth: 'basic'`
 * or `'none'` pass that as the third argument (no token is then needed).
 */
export async function openBoard(page: Page, omo: Omo, auth: 'token' | 'basic' | 'none' = 'token'): Promise<void> {
  await page.goto(entry(omo, auth))
  await expectBoardLive(page)
}

export async function expectBoardLive(page: Page): Promise<void> {
  await expect(page.locator('[data-board]')).toBeVisible()
  await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
}

/** The card of ticket `n` (hidden when its column is not the one shown on a narrow screen). */
export function card(page: Page, n: number) {
  return page.locator(`li[data-ticket="${n}"]`)
}

/** A column's panel by its title, e.g. `column(page, 'Ready For Dev')`. */
export function column(page: Page, title: ColumnTitle) {
  return page.locator(`section[data-drop-column="${KEYS[title]}"]`)
}

export async function expectCardIn(page: Page, n: number, title: ColumnTitle): Promise<void> {
  await expect(card(page, n)).toHaveCount(1)
  await expect(column(page, title).locator(`li[data-ticket="${n}"]`)).toHaveCount(1)
}

/** Ticket numbers per column in the order the page shows them. */
export async function boardCards(page: Page): Promise<Record<ColumnKey, number[]>> {
  return page.evaluate(() => {
    const out: Record<string, number[]> = {}
    for (const key of ['planning', 'ready', 'in_progress', 'review', 'done']) {
      const section = document.querySelector(`section[data-drop-column="${key}"]`)
      out[key] = [...(section?.querySelectorAll('li[data-ticket]') ?? [])].map((li) => Number(li.getAttribute('data-ticket')))
    }
    return out
  }) as Promise<Record<ColumnKey, number[]>>
}

interface ServerTickets {
  tickets: { number: number; column: ColumnKey }[]
}

/**
 * Waits until the board shows every open ticket and every loaded Done ticket
 * exactly once, in the column the server has it in, and nothing else.
 */
export async function expectBoardEqualsServer(page: Page, omo: Omo, timeout = 15_000): Promise<void> {
  await expect(async () => {
    const server = await omo.api<ServerTickets>('GET', '/api/tickets')
    const shown = await boardCards(page)
    const all = COLUMN_KEYS.flatMap((k) => shown[k])
    expect(new Set(all).size, 'a card is shown twice').toBe(all.length)
    expect(await page.locator('li[data-ticket]').count(), 'cards outside the columns').toBe(all.length)
    for (const key of COLUMN_KEYS) {
      const want = server.tickets.filter((t) => t.column === key).map((t) => t.number)
      if (key === 'done') {
        // The page may show fewer Done tickets than the server has, never others.
        for (const n of shown.done) expect(want, `#${n} is in Done on the page only`).toContain(n)
        if (want.length <= 50) expect([...shown.done].sort(numeric)).toEqual([...want].sort(numeric))
      } else {
        expect([...shown[key]].sort(numeric), `column ${key}`).toEqual([...want].sort(numeric))
      }
    }
  }).toPass({ timeout })
}

const numeric = (a: number, b: number) => a - b

/** Waits for a card to show its title; useful to know that an event has been applied. */
export async function expectCardTitle(page: Page, n: number, title: string): Promise<void> {
  await expect(card(page, n).locator('a')).toHaveText(title)
}

/** Sets a marker on the page that a reload would erase. */
export async function markPage(page: Page): Promise<void> {
  await page.evaluate(() => {
    ;(window as unknown as { __e2eMarker: number }).__e2eMarker = 1
  })
}

/** Fails when the page was reloaded since `markPage`. */
export async function expectNotReloaded(page: Page): Promise<void> {
  expect(await page.evaluate(() => (window as unknown as { __e2eMarker?: number }).__e2eMarker)).toBe(1)
}

/** Simulates the browser coming back online: the board reconnects at once instead of waiting for its backoff. */
export async function nudgeReconnect(page: Page): Promise<void> {
  await page.evaluate(() => window.dispatchEvent(new Event('online')))
}

// ---- what the browser saw -------------------------------------------------

export interface Observed {
  /** Every request URL (as the browser sends it: fragments never appear here). */
  requestUrls: string[]
  /** Every console message as `type: text`. */
  console: string[]
  pageErrors: string[]
}

/** Records requests, console messages and page errors; call before the first navigation. */
export function observe(target: Page | BrowserContext): Observed {
  const seen: Observed = { requestUrls: [], console: [], pageErrors: [] }
  target.on('request', (r) => seen.requestUrls.push(r.url()))
  target.on('console', (m) => seen.console.push(`${m.type()}: ${m.text()}`))
  if ('goto' in target) target.on('pageerror', (e) => seen.pageErrors.push(e.message))
  return seen
}

/** The address bar right now, fragment included. */
export async function addressBar(page: Page): Promise<string> {
  return page.evaluate(() => location.href)
}

export async function historyLength(page: Page): Promise<number> {
  return page.evaluate(() => history.length)
}

/** The URLs of all entries of the tab's session history (Chromium DevTools). */
export async function historyUrls(page: Page): Promise<string[]> {
  const cdp = await page.context().newCDPSession(page)
  try {
    const h = (await cdp.send('Page.getNavigationHistory')) as { entries: { url: string }[] }
    return h.entries.map((e) => e.url)
  } finally {
    await cdp.detach()
  }
}
