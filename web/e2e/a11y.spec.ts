import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { expectCardIn, openBoard } from './support/board'

async function violations(page: Page) {
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    test('the locked screen has no accessibility violations', async ({ page, omo }) => {
      await page.goto(omo.url('/'))
      await expect(page.getByRole('heading', { level: 1, name: 'omo is locked' })).toBeVisible()
      expect(await violations(page)).toEqual([])
    })

    test('the board has no accessibility violations', async ({ page, omo }) => {
      const a = await omo.createTicket({ title: 'A task in planning' })
      const story = await omo.createTicket({ title: 'A story', type: 'story' })
      const b = await omo.createTicket({ title: 'A task that is ready' })
      await omo.moveTicket(b, 'ready')
      await openBoard(page, omo)
      await expectCardIn(page, a, 'Planning')
      await expectCardIn(page, story, 'Planning')
      await expectCardIn(page, b, 'Ready For Dev')
      expect(await violations(page)).toEqual([])
    })

    test('the first steps on an empty board have no accessibility violations', async ({ page, omo }) => {
      await openBoard(page, omo)
      expect(await violations(page)).toEqual([])
    })
  })
}
