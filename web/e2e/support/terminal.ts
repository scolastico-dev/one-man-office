import { expect, type Locator, type Page } from '@playwright/test'
import type { Observed } from './board'

/** The xterm terminal inside `locator`. */
export function terminalIn(locator: Locator): Locator {
  return locator.locator('.xterm').first()
}

/**
 * Focuses the terminal's input, types `text` and presses Enter.
 *
 * An agent terminal starts read-only and then takes no key: the test has to
 * switch it with makeWritable first. Typing into a read-only terminal fails
 * here at once, instead of as a timeout on whatever the keys should have
 * caused.
 */
export async function typeInTerminal(page: Page, terminal: Locator, text: string): Promise<void> {
  const screen = terminal.locator('xpath=ancestor::*[contains(concat(" ", normalize-space(@class), " "), " tv__screen ")][1]')
  expect(await screen.getAttribute('data-readonly'), 'the terminal is read-only: call makeWritable before typing into it').toBeNull()
  await terminal.locator('.xterm-helper-textarea').focus()
  await page.keyboard.type(text)
  await page.keyboard.press('Enter')
}

/** Waits until the terminal's rows contain `text`. */
export async function expectTerminalText(terminal: Locator, text: string | RegExp): Promise<void> {
  await expect(terminal.locator('.xterm-rows')).toContainText(text)
}

/** No request and no websocket handshake the page made carried the login token in its address. */
export function expectNoTokenInUrls(observed: Observed, token: string): void {
  expect(token).not.toBe('')
  for (const url of observed.requestUrls) expect(url, 'a request address carries the token').not.toContain(token)
  for (const url of observed.websocketUrls) expect(url, 'a websocket address carries the token').not.toContain(token)
}

/** Switches an agent terminal inside `scope` from read-only to writable (it starts read-only); does nothing when it is writable. */
export async function makeWritable(scope: Locator): Promise<void> {
  const toggle = scope.locator('[data-terminal-mode]').first()
  if ((await toggle.getAttribute('data-writable')) === null) await toggle.click()
  await expect(toggle).toHaveAttribute('data-writable', '')
}
