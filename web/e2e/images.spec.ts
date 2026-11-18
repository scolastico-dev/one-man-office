// Images uploaded to tickets: by the button, a paste and a drop into a
// comment, in a description on the ticket page, by an agent; a refusal; a
// deleted image; phones; accessibility; and never a token in an address,
// image requests included.
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script } from './support/agents'
import { observe, openBoard } from './support/board'
import { expectNoTokenInUrls } from './support/terminal'
import { pick, png } from './support/images'

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const box = (page: Page) => panel(page).getByLabel('Write a comment')
const thread = (page: Page) => panel(page).locator('[data-thread]')

async function openTicket(page: Page, omo: Omo, n: number): Promise<void> {
  await openBoard(page, omo)
  await page.goto(omo.url(`/${n}`))
  await expect(thread(page)).toBeVisible()
}

/** Puts files on a paste or drop event of the element, as the browser would. */
async function sendFiles(page: Page, selector: string, kind: 'paste' | 'drop', name: string): Promise<void> {
  const bytes = [...png()]
  await page.evaluate(
    ({ selector, kind, name, bytes }) => {
      const el = document.querySelector(selector) as HTMLTextAreaElement
      el.focus()
      el.setSelectionRange(el.value.length, el.value.length)
      const dt = new DataTransfer()
      dt.items.add(new File([new Uint8Array(bytes)], name, { type: 'image/png' }))
      const ev =
        kind === 'paste'
          ? new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true })
          : new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true })
      el.dispatchEvent(ev)
    },
    { selector, kind, name, bytes },
  )
}

const IMG_REF = /!\[([^\]]*)\]\(omo-image:([0-9a-f-]{36})\)/

test('the user adds images to a comment by the button, a paste and a drop; they show in the thread and open full size', async ({ page, omo }) => {
  const seen = observe(page)
  const n = await omo.createTicket({ title: 'Look at this' })
  await openTicket(page, omo, n)
  await box(page).fill('By the button:')
  await pick(page, panel(page), [{ name: 'button shot.png', mimeType: 'image/png', buffer: png() }])
  await expect(box(page)).toHaveValue(/By the button:\n!\[button shot\.png\]\(omo-image:[0-9a-f-]{36}\)\n/)
  await sendFiles(page, 'dialog[data-ticket-panel] [data-comment-box]', 'paste', 'pasted.png')
  await expect(box(page)).toHaveValue(/!\[pasted\.png\]\(omo-image:/)
  await sendFiles(page, 'dialog[data-ticket-panel] [data-comment-box]', 'drop', 'dropped.png')
  await expect(box(page)).toHaveValue(/!\[dropped\.png\]\(omo-image:/)
  await expect(box(page)).not.toHaveValue(/Uploading/)
  await panel(page).locator('[data-send]').click()

  const body = thread(page).locator('[data-comment-body]')
  for (const name of ['button shot.png', 'pasted.png', 'dropped.png']) {
    const img = body.getByRole('img', { name })
    await expect(img).toBeVisible()
    await expect(img).toHaveAttribute('src', /^blob:/)
    await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBe(48)
  }
  // Fits the column.
  const fits = await body.locator('img').first().evaluate((el) => el.getBoundingClientRect().width <= el.closest('[data-comment-body]')!.getBoundingClientRect().width)
  expect(fits).toBe(true)

  // Full size in a dialog, reachable by keyboard, back with Escape.
  await body.getByRole('button', { name: 'pasted.png' }).focus()
  await page.keyboard.press('Enter')
  const box2 = page.locator('dialog[data-image-lightbox]')
  await expect(box2).toBeVisible()
  await expect(box2.getByRole('heading', { name: 'pasted.png' })).toBeVisible()
  await expect(box2.getByRole('img', { name: 'pasted.png' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(box2).toHaveCount(0)
  await expect(panel(page)).toBeVisible()

  // Stored under random ids, with the original names kept as alt text only.
  const images = await omo.api<{ name: string; path: string }[]>('GET', `/api/tickets/${n}/images`)
  expect(images.map((i) => i.name).sort()).toEqual(['button shot.png', 'dropped.png', 'pasted.png'])
  for (const i of images) expect(i.path).toMatch(/\/images\/[0-9a-f-]{36}\.png$/)
  expect(seen.requestUrls.some((u) => /\/api\/images\/[0-9a-f-]{36}\/content$/.test(u))).toBe(true)
  expectNoTokenInUrls(seen, omo.token)
})

test('a refused upload shows the server sentence in place and inserts nothing', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'No scripts please' })
  await openTicket(page, omo, n)
  await box(page).fill('Keep me')
  await pick(page, panel(page), [{ name: 'evil.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>') }])
  const errors = panel(page).locator('[data-image-errors]')
  await expect(errors).toHaveText('Not added: evil.svg: this file is not a PNG, JPEG, GIF or WebP image; omo takes only those (SVG is refused, it can carry scripts)')
  await expect(box(page)).toHaveValue('Keep me')
  expect(await omo.api<unknown[]>('GET', `/api/tickets/${n}/images`)).toEqual([])
})

test('a deleted image shows its alt text with a note', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Here today' })
  await openTicket(page, omo, n)
  await pick(page, panel(page), [{ name: 'gone soon.png', mimeType: 'image/png', buffer: png() }])
  await expect(box(page)).toHaveValue(IMG_REF)
  const id = IMG_REF.exec(await box(page).inputValue())![2]!
  await panel(page).locator('[data-send]').click()
  const body = thread(page).locator('[data-comment-body]')
  await expect(body.getByRole('img', { name: 'gone soon.png' })).toBeVisible()
  await omo.api('DELETE', `/api/images/${id}`)
  // The open view hears of it at once; a fresh one reads it as missing.
  await expect(body).toContainText('gone soon.png (image missing)')
  await expect(body.locator('img')).toHaveCount(0)
  await page.reload()
  await expect(thread(page).locator('[data-comment-body]')).toContainText('gone soon.png (image missing)')
})

test('an image in the description shows on the ticket page and in the description history', async ({ page, omo }) => {
  const seen = observe(page)
  const n = await omo.createTicket({ title: 'Picture this' })
  await openBoard(page, omo)
  await page.goto(omo.url(`/ticket/${n}`))
  const ticketPage = page.locator('[data-ticket-page]')
  await ticketPage.locator('[data-edit-description]').click()
  const editor = ticketPage.locator('[data-description-editor]')
  await editor.getByLabel('Description (Markdown)').fill('The layout:')
  await pick(page, editor, [{ name: 'layout.png', mimeType: 'image/png', buffer: png() }])
  await expect(editor.getByLabel('Description (Markdown)')).toHaveValue(/The layout:\n!\[layout\.png\]\(omo-image:/)
  await editor.locator('[data-save-description]').click()
  const view = ticketPage.locator('[data-description]')
  await expect(view.getByRole('img', { name: 'layout.png' })).toBeVisible()
  await ticketPage.locator('[data-open-history]').click()
  const history = page.getByRole('dialog').filter({ has: page.locator('[data-history-preview]') })
  await expect(history.locator('[data-history-preview]').getByRole('img', { name: 'layout.png' })).toBeVisible()
  expectNoTokenInUrls(seen, omo.token)
})

test('on a phone the image fits and nothing scrolls sideways; no axe violation in either theme', async ({ page, omo }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const n = await omo.createTicket({ title: 'Small screen' })
  await openTicket(page, omo, n)
  await pick(page, panel(page), [{ name: 'wide.png', mimeType: 'image/png', buffer: png(1600, 200) }])
  await expect(box(page)).toHaveValue(IMG_REF)
  await panel(page).locator('[data-send]').click()
  const img = thread(page).locator('[data-comment-body]').getByRole('img', { name: 'wide.png' })
  await expect(img).toBeVisible()
  const width = await img.evaluate((el) => el.getBoundingClientRect().width)
  expect(width).toBeLessThanOrEqual(390)
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0)
  for (const colorScheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme })
    const results = await new AxeBuilder({ page }).include('dialog[data-ticket-panel]').analyze()
    expect(results.violations, colorScheme).toEqual([])
  }
  await img.click()
  const lightbox = page.locator('dialog[data-image-lightbox]')
  await expect(lightbox).toBeVisible()
  for (const colorScheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme })
    const results = await new AxeBuilder({ page }).include('dialog[data-image-lightbox]').analyze()
    expect(results.violations, `lightbox ${colorScheme}`).toEqual([])
  }
})

test.describe('an agent adds an image', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'image|$TICKET|agent shot.png|From the agent:', 'hang') } } })

  test('it shows in the thread like the user one', async ({ page, omo }) => {
    const seen = observe(page)
    const n = await omo.createTicket({ title: 'Agent at work' })
    await openTicket(page, omo, n)
    await omo.moveTicket(n, 'ready')
    const body = thread(page).locator('[data-comment-body]', { hasText: 'From the agent:' })
    await expect(body.getByRole('img', { name: 'agent shot.png' })).toBeVisible({ timeout: 30_000 })
    const [image] = await omo.api<{ author: { role: string }; name: string }[]>('GET', `/api/tickets/${n}/images`)
    expect(image).toMatchObject({ author: { role: 'developer' }, name: 'agent shot.png' })
    expectNoTokenInUrls(seen, omo.token)
    await expectNoAgentProcesses(omo)
  })
})
