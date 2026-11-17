// Screenshots of the sample board (/__sample) for judging the look:
// web/screenshots/{dark,light}-{1440,1024,390}-{board,empty,drag}.png,
// {dark,light}-{1440,390}-{sidebar-*,panel-*,bell}.png for the sidebar, the
// ticket panel's tabs and the notifications,
// {dark,light}-{1440,390}-settings-<section>.png for the settings sample
// (plus settings-picker, settings-usage-script and -error with the open
// script's test, settings-skills-editor, settings-plugins-log and, narrow,
// settings-list),
// {dark,light}-{1440,390}-{sidebar-usage,panel-actions}.png for the usage
// block of the Agents tab and the extra ticket actions, and a few focus and
// font variants. Starts its own Vite dev server on a free port and stops it
// afterwards. Run with `npm run screenshots`; `npm run screenshots -- usage`
// takes only the screenshots whose file name contains "usage".
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
  /** Scrolled into view before the screenshot. */
  scrollTo?: string
  /** Focus this element, then press these keys, to show keyboard focus. */
  keys?: { focus: string; press: string[] }
}

const shots: Shot[] = []
for (const theme of themes) {
  for (const size of sizes) {
    for (const [view, query] of Object.entries(views)) {
      shots.push({ file: `${theme}-${size.width}-${view}.png`, query: [`theme=${theme}`, query].filter(Boolean).join('&'), ...size })
    }
  }
}
// The sidebar, the ticket panel's tabs and the notifications, wide and narrow.
const surfaces: Record<string, { query: string; scrollTo?: string }> = {
  'sidebar-agents': { query: 'sidebar=open&tab=agents' },
  'sidebar-ceo': { query: 'sidebar=open&tab=ceo' },
  'sidebar-terminals': { query: 'sidebar=open&tab=terminals' },
  'sidebar-usage': { query: 'sidebar=open&tab=agents&usage=1' },
  'panel-comments': { query: 'panel=13&tab=comments', scrollTo: '.ptabs' },
  'panel-diff': { query: 'panel=13&tab=diff', scrollTo: '.ptabs' },
  'panel-agents': { query: 'panel=13&tab=agents', scrollTo: '.ptabs' },
  'panel-actions': { query: 'panel=13&usage=1' },
  bell: { query: 'bell=open' },
}
for (const theme of themes) {
  for (const size of [sizes[0]!, sizes[2]!]) {
    for (const [view, v] of Object.entries(surfaces)) {
      shots.push({ file: `${theme}-${size.width}-${view}.png`, query: `theme=${theme}&${v.query}`, ...size, ...(v.scrollTo ? { scrollTo: v.scrollTo } : {}) })
    }
  }
  // Keyboard focus on a tab after an arrow key, and on the sidebar's splitter.
  shots.push({
    file: `${theme}-1440-sidebar-tabs-focus.png`,
    query: `theme=${theme}&sidebar=open&tab=agents`,
    width: 1440,
    height: 900,
    keys: { focus: '[role="tablist"][aria-label="Sidebar"] [aria-selected="true"]', press: ['ArrowRight'] },
  })
  shots.push({
    file: `${theme}-1440-sidebar-splitter-focus.png`,
    query: `theme=${theme}&sidebar=open&tab=agents`,
    width: 1440,
    height: 900,
    keys: { focus: '[role="separator"]', press: ['ArrowRight', 'ArrowRight'] },
  })
}
// The settings sample: every section, the folder picker, and at 390 px the section list.
const settingsSections = ['projects', 'agents', 'roles', 'usage', 'prompts', 'skills', 'environment', 'limits', 'review', 'plugins', 'general']
for (const theme of themes) {
  for (const size of [sizes[0]!, sizes[2]!]) {
    for (const section of settingsSections) {
      shots.push({ file: `${theme}-${size.width}-settings-${section}.png`, query: `theme=${theme}&settings=${section}`, ...size })
    }
    shots.push({ file: `${theme}-${size.width}-settings-picker.png`, query: `theme=${theme}&settings=projects&picker=open`, ...size })
    shots.push({ file: `${theme}-${size.width}-settings-usage-script.png`, query: `theme=${theme}&settings=usage`, ...size, scrollTo: '[aria-label="Test result"]' })
    shots.push({ file: `${theme}-${size.width}-settings-usage-error.png`, query: `theme=${theme}&settings=usage&test=error`, ...size, scrollTo: '[aria-label="Test result"]' })
  }
  // The open item of Skill sets and Plugins, further down.
  for (const size of [sizes[0]!, sizes[2]!]) {
    shots.push({ file: `${theme}-${size.width}-settings-skills-editor.png`, query: `theme=${theme}&settings=skills`, ...size, scrollTo: '.xs-editor .xs-state' })
    shots.push({ file: `${theme}-${size.width}-settings-plugins-log.png`, query: `theme=${theme}&settings=plugins`, ...size, scrollTo: '.xs-editor .xs-actions' })
  }
  shots.push({ file: `${theme}-390-settings-list.png`, query: `theme=${theme}&settings=list`, ...sizes[2]! })
  // Lower down the long pages: the models of a profile, and the environment table with its check.
  shots.push({ file: `${theme}-1440-settings-agents-models.png`, query: `theme=${theme}&settings=agents`, ...sizes[0]!, scrollTo: 'fieldset:nth-of-type(2)' })
  shots.push({ file: `${theme}-390-settings-agents-env.png`, query: `theme=${theme}&settings=agents`, ...sizes[2]!, scrollTo: '.env' })
  // Keyboard focus on a row button of a list field after a move.
  shots.push({
    file: `${theme}-1440-settings-list-focus.png`,
    query: `theme=${theme}&settings=projects`,
    width: 1440,
    height: 900,
    keys: { focus: '[data-row="2"] [data-part="up"]', press: ['Enter'] },
  })
}
shots.push({ file: 'dark-1440-board-system-font.png', query: 'theme=dark&font=system', width: 1440, height: 900 })
shots.push({ file: 'dark-1440-board-sidebar-open.png', query: 'theme=dark&sidebar=open', width: 1440, height: 900 })
shots.push({ file: 'dark-390-board-in-progress.png', query: 'theme=dark&column=in_progress', width: 390, height: 844 })
shots.push({ file: 'dark-1440-board-focus.png', query: 'theme=dark', width: 1440, height: 900, focus: 13 })
shots.push({ file: 'light-1440-board-focus.png', query: 'theme=light', width: 1440, height: 900, focus: 13 })

// An argument keeps only the screenshots whose file name contains it.
const only = process.argv[2]
const taken = only ? shots.filter((s) => s.file.includes(only)) : shots

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
  for (const shot of taken) {
    const context = await browser.newContext({ viewport: { width: shot.width, height: shot.height }, reducedMotion: 'reduce' })
    const page = await context.newPage()
    await page.goto(`${new URL('/__sample', base).href}?${shot.query}`)
    await page.locator('[data-board], [data-settings]').first().waitFor()
    await page.evaluate('document.fonts.ready')
    // The page itself must never scroll sideways.
    const overflow = Number(await page.evaluate('document.documentElement.scrollWidth - document.documentElement.clientWidth'))
    if (overflow > 0) {
      problems++
      console.error(`${shot.file}: the page scrolls sideways by ${overflow}px`)
    }
    if (shot.query.includes('panel=')) await page.locator('[data-ticket-panel] .ptabs').waitFor()
    if (shot.scrollTo) {
      // Scroll only the nearest scrolling box, so the dialog's own head stays in view.
      await page
        .locator(shot.scrollTo)
        .first()
        .evaluate((el) => {
          const view = el.ownerDocument.defaultView
          let box = el.parentElement
          while (box && view && !/(auto|scroll)/.test(view.getComputedStyle(box).overflowY)) box = box.parentElement
          if (box) box.scrollTop += el.getBoundingClientRect().top - box.getBoundingClientRect().top - 16
        })
    }
    if (shot.keys) {
      // A pointer-free focus first, so the browser shows the keyboard focus ring.
      await page.locator(shot.keys.focus).first().focus()
      for (const key of shot.keys.press) await page.keyboard.press(key)
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
