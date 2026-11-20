// The draft box below an agent terminal, and agent terminals being read-only
// by default. The fake agent reads its terminal like a full-screen CLI with an
// input line and bracketed paste (OMO_FAKE_BRACKETED_PASTE): a message pasted
// as one piece arrives as one line, its line breaks shown as ⏎, and every
// submitted line is echoed as "fake: typed: <line>".
import type { Locator, Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { openBoard } from './support/board'
import { png } from './support/images'
import { expectTerminalText, makeWritable, terminalIn } from './support/terminal'

test.use({
  viewport: { width: 1440, height: 900 },
  omoOptions: {
    agentEnv: { OMO_FAKE_BRACKETED_PASTE: '1' },
    agents: { developer: script('ready', 'print|hello from fake', 'await-line|alpha⏎beta|10m', 'await-line|/images/terminal/|10m', 'print|got both', 'hang') },
  },
})

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')

async function openAgent(page: Page, omo: Parameters<typeof openBoard>[1]): Promise<{ n: number; box: Locator; term: Locator }> {
  const n = await omo.createTicket({ title: 'Write to the developer' })
  await omo.moveTicket(n, 'ready')
  const session = await waitForSession(omo, n, 'developer')
  await openBoard(page, omo)
  await page.goto(omo.url(`/${n}?tab=agents`))
  await panel(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
  const term = terminalIn(panel(page))
  await expectTerminalText(term, 'hello from fake')
  return { n, box: panel(page).locator('[data-terminal-draft]'), term }
}

test('keys and paste do nothing in a read-only agent terminal; the draft box is there and sends as one message', async ({ page, omo }) => {
  const { n, box, term } = await openAgent(page, omo)
  await expect(panel(page).locator('[data-terminal-mode]')).toHaveText('Read only')

  // Typing into the read-only terminal reaches nobody.
  await term.locator('.xterm-helper-textarea').focus()
  await page.keyboard.type('zzz')
  await page.keyboard.press('Enter')
  // Nothing to wait for here: that the keys reached nobody is read below, once
  // a later line of the same terminal (omo's own) has been echoed. The program
  // echoes every line in the order it got them, so a line typed above would
  // already be on the screen before that one.

  // A draft of two lines, and omo types a line into the terminal meanwhile.
  const field = box.locator('[data-draft-field]')
  await field.click()
  await page.keyboard.type('alpha')
  await page.keyboard.press('Shift+Enter')
  await page.keyboard.type('beta')
  await expect(field).toHaveValue('alpha\nbeta')
  await omo.api('POST', `/api/tickets/${n}/comments`, { body: '@developer a note while you draft' })
  await expectTerminalText(term, /typed: omo:/)
  await expect(term.locator('.xterm-rows')).not.toContainText('typed: zzz')
  // The line did not touch the draft or its focus.
  await expect(field).toHaveValue('alpha\nbeta')
  await expect(field).toBeFocused()

  // Enter sends it as one message, even though the terminal is read-only.
  await page.keyboard.press('Enter')
  await expectTerminalText(term, 'typed: alpha⏎beta')
  await expect(field).toHaveValue('')
})

test('inline images can be deleted and are pasted between their surrounding text in order', async ({ page, omo }) => {
  const { box, term } = await openAgent(page, omo)
  const field = box.locator('[data-draft-field]')
  const pasteImage = (name: string) =>
    field.evaluate(
      (el, { name, bytes }) => {
        const dt = new DataTransfer()
        dt.items.add(new File([new Uint8Array(bytes)], name, { type: 'image/png' }))
        el.dispatchEvent(new ClipboardEvent('paste', { bubbles: true, cancelable: true, clipboardData: dt }))
      },
      { name, bytes: [...png()] },
    )
  // The first message the script waits for.
  await field.fill('alpha\nbeta')
  await field.press('Enter')
  await expectTerminalText(term, 'typed: alpha⏎beta')

  await pasteImage('discard me.png')
  await expect(box.locator('[data-draft-attachment]')).toHaveCount(1)
  await expect(field).toHaveValue('[image 1]')
  await field.press('Backspace')
  await expect(field).toHaveValue('')
  await expect(box.locator('[data-draft-attachment]')).toHaveCount(0)

  await field.fill('before after')
  await field.evaluate((el) => (el as HTMLTextAreaElement).setSelectionRange(7, 7))
  await pasteImage('keep me.png')
  await expect(box.locator('[data-draft-send]')).toBeEnabled()
  await expect(field).toHaveValue('before [image 2]after')
  // Typing with the caret inside the marker keeps its image and snaps after it.
  await field.evaluate((el) => (el as HTMLTextAreaElement).setSelectionRange(11, 11))
  await field.pressSequentially(' between ')
  await pasteImage('keep this too.png')
  await expect(box.locator('[data-draft-send]')).toBeEnabled()
  await expect(field).toHaveValue('before [image 2] between [image 3]after')
  await expect(box.locator('[data-draft-attachment]')).toHaveCount(2)
  await field.press('Enter')
  await expectTerminalText(term, 'got both')
  await expectTerminalText(term, /typed: before .*images\/terminal\/[^ ]+.* between .*images\/terminal\/[^ ]+.*after/)
  await expect(term.locator('.xterm-rows')).not.toContainText('[image 2]')
  await expect(box.locator('[data-draft-attachment]')).toHaveCount(0)
  await expect(field).toHaveValue('')
})

test('the toggle makes the terminal writable and the draft survives a reload', async ({ page, omo }) => {
  const { n, box } = await openAgent(page, omo)
  await box.locator('[data-draft-field]').fill('unfinished thought')
  await makeWritable(panel(page))
  await page.reload()
  const session = await waitForSession(omo, n, 'developer')
  await panel(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
  await expect(panel(page).locator('[data-terminal-mode]')).toHaveText('Writable')
  await expect(panel(page).locator('[data-draft-field]')).toHaveValue('unfinished thought')
  await omo.api('POST', '/api/agents/stop-all', {})
  await expectNoAgentProcesses(omo)
})

test('single click focuses the draft and dragging still selects terminal output', async ({ page, omo }) => {
  const { box, term } = await openAgent(page, omo)
  const field = box.locator('[data-draft-field]')
  const row = term.locator('.xterm-rows > div').filter({ hasText: 'hello from fake' }).last()
  // Rows are xterm's text mirror; the screen above them receives real pointers.
  await term.locator('.xterm-screen').click({ position: { x: 8, y: 6 } })
  await expect(field).toBeFocused()
  await panel(page).locator('[data-terminal-mode]').focus()
  const bounds = await row.boundingBox()
  if (!bounds) throw new Error('terminal output has no bounds')
  await page.mouse.move(bounds.x + 2, bounds.y + bounds.height / 2)
  await page.mouse.down()
  await page.mouse.move(bounds.x + 100, bounds.y + bounds.height / 2, { steps: 5 })
  await page.mouse.up()
  await expect(term.locator('.xterm-selection div')).not.toHaveCount(0)
  await expect(field).not.toBeFocused()
})

for (const width of [1440, 390]) {
  test(`Send follows the draft field height while growing and shrinking at ${width} px`, async ({ page, omo }) => {
    await page.setViewportSize({ width, height: 900 })
    const { box } = await openAgent(page, omo)
    const field = box.locator('[data-draft-field]')
    // Font loading can change wrapping and therefore the field's rendered height.
    await page.evaluate(() => document.fonts.ready.then(() => undefined))
    let single = 0
    let grown = 0
    for (const [index, text] of ['one line', 'one\ntwo\nthree\nfour', Array.from({ length: 40 }, (_, i) => `line ${i}`).join('\n'), 'short again'].entries()) {
      await field.fill(text)
      await expect(field).toHaveValue(text)
      // Wait for the component's next-tick grow work, then compare actual layout.
      await expect.poll(() => box.evaluate((el) => {
        const textarea = el.querySelector<HTMLTextAreaElement>('[data-draft-field]')!
        return textarea.style.height === `${textarea.scrollHeight}px`
      })).toBe(true)
      const heights = await box.evaluate((el) => {
        const field = el.querySelector<HTMLTextAreaElement>('[data-draft-field]')!
        return { field: field.getBoundingClientRect().height, button: el.querySelector('[data-draft-send]')!.getBoundingClientRect().height, scroll: field.scrollHeight }
      })
      expect(heights.button).toBe(heights.field)
      if (index === 0) single = heights.field
      if (index === 1) {
        grown = heights.field
        expect(grown).toBeGreaterThan(single)
      }
      if (index === 2) {
        expect(heights.field).toBeGreaterThan(grown)
        expect(heights.scroll).toBeGreaterThan(heights.field)
      }
      if (index === 3) expect(heights.field).toBe(single)
    }
  })
}

test('a real click returns focus to a Writable agent terminal and Escape reaches its socket', async ({ page, omo }) => {
  let escapeSent!: () => void
  const escaped = new Promise<void>((resolve) => { escapeSent = resolve })
  page.on('websocket', (socket) => {
    if (!socket.url().includes('/api/agents/') || !socket.url().endsWith('/terminal')) return
    socket.on('framesent', ({ payload }) => {
      if (payload.toString() === '\x1b') escapeSent()
    })
  })
  const { box, term } = await openAgent(page, omo)
  await makeWritable(panel(page))
  await box.locator('[data-draft-field]').click()
  await term.locator('.xterm-screen').click({ position: { x: 8, y: 6 } })
  await expect(term.locator('.xterm-helper-textarea')).toBeFocused()
  await page.keyboard.press('Escape')
  await escaped
})
