// The wheel audit itself: every box it names gets its wheel at a point that really is on that box, also after an
// earlier wheel handed its scroll to a box around it, and every scroll offset is back where it was.
import { expect, test } from './support/omo'
import { auditWheels } from './support/wheelAudit'

const page$ = `
<style>
  body { margin: 0 }
  .outer { width: 400px; height: 240px; overflow-y: auto }
  .box-a { height: 60px; overflow: auto }
  .box-a > div { width: 900px; height: 50px }
  .box-b { height: 60px; overflow-y: auto }
  .box-b > div { height: 400px }
  .spacer { height: 600px }
</style>
<div class="outer">
  <div class="box-a"><div>wide, and no room to scroll up or down</div></div>
  <div class="box-b"><div>tall</div></div>
  <div class="spacer">spacer</div>
</div>`

test('every box of the audit gets its wheel on the box itself and the offsets come back', async ({ page }) => {
  await page.setViewportSize({ width: 800, height: 600 })
  await page.setContent(page$)
  await page.evaluate(() => {
    const most: Record<string, number> = ((window as unknown as { most: Record<string, number> }).most = { aLeft: 0, bTop: 0, outerTop: 0 })
    const watch = (sel: string, read: (el: Element) => number, key: string) =>
      document.querySelector(sel)!.addEventListener('scroll', (e) => (most[key] = Math.max(most[key] ?? 0, read(e.target as Element))))
    watch('.box-a', (el) => el.scrollLeft, 'aLeft')
    watch('.box-b', (el) => el.scrollTop, 'bTop')
    watch('.outer', (el) => el.scrollTop, 'outerTop')
  })

  const audit = await auditWheels(page, '.outer', 'the fixture')

  // A box that scrolls one way has the other axis auto too, so each has both.
  expect(audit.visited.sort()).toEqual(['div.box-a x', 'div.box-a y', 'div.box-b x', 'div.box-b y', 'div.outer x', 'div.outer y'].sort())
  expect(audit.bad.filter((b) => b.includes('no point'))).toEqual([])
  const most = await page.evaluate(() => (window as unknown as { most: Record<string, number> }).most)
  // The sideways wheel moved box A, the vertical one over B moved B (not whatever sat where B had been before the box above handed its scroll on), and the outer box was moved.
  expect(most.aLeft).toBeGreaterThan(0)
  expect(most.bTop).toBeGreaterThan(0)
  expect(most.outerTop).toBeGreaterThan(0)
  expect(await page.evaluate(() => [...document.querySelectorAll<HTMLElement>('.outer, .outer *')].map((el) => [el.scrollTop, el.scrollLeft]).flat().every((v) => v === 0))).toBe(true)
  expect(await page.evaluate(() => document.querySelectorAll('[data-wheel-audit]').length)).toBe(0)
})

test('an audit of a scope with no scroll box is an error', async ({ page }) => {
  await page.setContent('<div class="plain">nothing scrolls here</div>')
  await expect(auditWheels(page, '.plain', 'a plain box')).rejects.toThrow('nothing to scroll in a plain box')
})
