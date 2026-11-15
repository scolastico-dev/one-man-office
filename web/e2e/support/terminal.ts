import { expect, type Locator, type Page } from '@playwright/test'
import type { Observed } from './board'

/** The xterm terminal inside `locator`. */
export function terminalIn(locator: Locator): Locator {
  return locator.locator('.xterm').first()
}

/** Focuses the terminal's input, types `text` and presses Enter. */
export async function typeInTerminal(page: Page, terminal: Locator, text: string): Promise<void> {
  await terminal.locator('.xterm-helper-textarea').focus()
  await page.keyboard.type(text)
  await page.keyboard.press('Enter')
}

/** Waits until the terminal's rows contain `text`. */
export async function expectTerminalText(terminal: Locator, text: string | RegExp): Promise<void> {
  await expect(terminal.locator('.xterm-rows')).toContainText(text)
}

/** No request the page made carried the login token in its address. */
export function expectNoTokenInUrls(observed: Observed, token: string): void {
  expect(token).not.toBe('')
  for (const url of observed.requestUrls) expect(url, 'a request address carries the token').not.toContain(token)
}
