// Screenshots of the sample board for judging the look:
// web/screenshots/{dark,light}-{1440,1024,390}-{board,empty,drag}.png and
// dark-1440-board-system-font.png. Starts its own Vite dev server on a free
// port and stops it afterwards. Run with `npm run screenshots`.
import { mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import AxeBuilder from '@axe-core/playwright'
import { chromium } from '@playwright/test'
import { createServer } from 'vite'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const out = join(root, 'screenshots')

const sizes = [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
]
const views: Record<string, string> = { board: '', empty: 'empty=1', drag: 'drag=planning' }
const themes = ['dark', 'light'] as const

interface Shot {
  file: string
  query: string
  width: number
  height: number
  /** Keyboard focus on this ticket's card before the screenshot. */
  focus?: number
}

const shots: Shot[] = []
for (const theme of themes) {
  for (const size of sizes) {
    for (const [view, query] of Object.entries(views)) {
      shots.push({ file: `${theme}-${size.width}-${view}.png`, query: [`theme=${theme}`, query].filter(Boolean).join('&'), ...size })
    }
  }
}
shots.push({ file: 'dark-1440-board-system-font.png', query: 'theme=dark&font=system', width: 1440, height: 900 })
shots.push({ file: 'dark-1440-board-sidebar-open.png', query: 'theme=dark&sidebar=open', width: 1440, height: 900 })
shots.push({ file: 'dark-390-board-in-progress.png', query: 'theme=dark&column=in_progress', width: 390, height: 844 })
shots.push({ file: 'dark-1440-board-focus.png', query: 'theme=dark', width: 1440, height: 900, focus: 13 })
shots.push({ file: 'light-1440-board-focus.png', query: 'theme=light', width: 1440, height: 900, focus: 13 })

mkdirSync(out, { recursive: true })

const server = await createServer({ root, configFile: join(root, 'vite.config.ts'), server: { port: 0, strictPort: false }, logLevel: 'warn' })
await server.listen()
const base = server.resolvedUrls?.local[0]
if (!base) throw new Error('the dev server did not report its address')

let problems = 0
const browser = await chromium.launch().catch(async (err: unknown) => {
  await server.close()
  throw err
})
try {
  for (const shot of shots) {
    const context = await browser.newContext({ viewport: { width: shot.width, height: shot.height }, reducedMotion: 'reduce' })
    const page = await context.newPage()
    await page.goto(`${base}?${shot.query}`)
    await page.locator('[data-board]').waitFor()
    await page.evaluate('document.fonts.ready')
    // The page itself must never scroll sideways.
    const overflow = Number(await page.evaluate('document.documentElement.scrollWidth - document.documentElement.clientWidth'))
    if (overflow > 0) {
      problems++
      console.error(`${shot.file}: the page scrolls sideways by ${overflow}px`)
    }
    if (shot.focus !== undefined) {
      // Tab from the card before, so the browser shows its keyboard focus ring.
      await page.locator(`[data-ticket="${shot.focus}"]`).locator('xpath=preceding-sibling::li[1]').locator('a').focus()
      await page.keyboard.press('Tab')
      await page.keyboard.press('Tab')
    }
    // Accessibility check of everything but the drag state, whose greyed columns are meant to recede.
    if (!shot.query.includes('drag=')) {
      const result = await new AxeBuilder({ page }).analyze()
      for (const v of result.violations) {
        problems++
        console.error(`${shot.file}: ${v.id}: ${v.help} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`)
      }
    }
    await page.screenshot({ path: join(out, shot.file) })
    await context.close()
    console.log(`wrote screenshots/${shot.file}`)
  }
} finally {
  await browser.close()
  await server.close()
}
if (problems > 0) process.exit(1)
