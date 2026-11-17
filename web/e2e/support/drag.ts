import { expect, type Page } from '@playwright/test'
import { card, COLUMN_KEYS, type ColumnKey, type ColumnTitle } from './board'

const KEYS: Record<ColumnTitle, ColumnKey> = {
  Planning: 'planning',
  'Ready For Dev': 'ready',
  'In Progress': 'in_progress',
  Review: 'review',
  Done: 'done',
}

/** Steps of every pointer movement, so the page sees a drag rather than a jump. */
const STEPS = 10

export interface DropAt {
  /** Drop right above this card (in the target column). */
  beforeCard?: number
  /** Drop on the column's tab of the switcher (narrow screens) instead of its panel. */
  tab?: boolean
}

async function box(page: Page, selector: string) {
  const b = await page.locator(selector).first().boundingBox()
  if (b === null) throw new Error(`${selector} is not on screen`)
  return b
}

/** Presses the mouse on card `n` and moves it far enough to pick the card up. */
export async function pickUp(page: Page, n: number): Promise<void> {
  const c = card(page, n)
  await c.scrollIntoViewIfNeeded()
  const b = await c.boundingBox()
  if (b === null) throw new Error(`card #${n} is not on screen`)
  // The top left corner: the number, under the title link, away from the chips.
  const x = b.x + 12
  const y = b.y + 10
  await page.mouse.move(x, y)
  await page.mouse.down()
  await page.mouse.move(x + 12, y + 6, { steps: 3 })
  await expect(page.locator('[data-drag-ghost]')).toBeVisible()
}

/** Moves the picked-up card over a column (or above one of its cards), in ten steps. */
export async function moveOver(page: Page, to: ColumnTitle, opts: DropAt = {}): Promise<void> {
  const key = KEYS[to]
  if (opts.tab) {
    const b = await box(page, `[role="tab"][data-drop-column="${key}"]`)
    await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: STEPS })
    return
  }
  const col = await box(page, `section[data-drop-column="${key}"]`)
  const x = col.x + col.width / 2
  if (opts.beforeCard !== undefined) {
    const target = await box(page, `section[data-drop-column="${key}"] li[data-ticket="${opts.beforeCard}"]`)
    await page.mouse.move(x, target.y + 4, { steps: STEPS })
    return
  }
  // Low in the column, below its cards.
  await page.mouse.move(x, col.y + col.height - 12, { steps: STEPS })
}

/** Moves the pointer somewhere that is no column: the top bar. */
export async function moveOutside(page: Page): Promise<void> {
  const b = await box(page, '[data-board]')
  await page.mouse.move(b.x + b.width / 2, Math.max(2, b.y - 30), { steps: STEPS })
}

export async function release(page: Page): Promise<void> {
  await page.mouse.up()
  await expect(page.locator('[data-drag-ghost]')).toHaveCount(0)
}

/** Drags card `n` onto a column with the mouse, in ten intermediate moves, and lets go. */
export async function dragCard(page: Page, n: number, to: ColumnTitle, opts: DropAt = {}): Promise<void> {
  await pickUp(page, n)
  await moveOver(page, to, opts)
  await release(page)
}

/** While a card is dragged: exactly these columns are greyed out (and say why). */
export async function expectGreyed(page: Page, columnTitles: ColumnTitle[]): Promise<void> {
  const greyed = new Set(columnTitles.map((t) => KEYS[t]))
  for (const key of COLUMN_KEYS) {
    const section = page.locator(`section[data-drop-column="${key}"]`)
    if (greyed.has(key)) {
      await expect(section, `${key} is greyed`).toHaveAttribute('aria-disabled', 'true')
      await expect(section.locator('[data-reason]')).not.toHaveText('')
    } else {
      await expect(section, `${key} accepts the card`).not.toHaveAttribute('aria-disabled', 'true')
      await expect(section.locator('[data-drop-action]')).not.toHaveText('')
    }
  }
}
