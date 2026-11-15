import { expect, test } from './support/omo'
import { openBoard } from './support/board'

test.use({ omoOptions: { noProject: true } })

test('a fresh home shows five columns titled Planning, Ready For Dev, In Progress, Review, Done', async ({ page, omo }) => {
  await openBoard(page, omo)
  const titles = page.locator('section[data-drop-column] h2')
  await expect(titles).toHaveCount(5)
  await expect(titles).toHaveText([/^Planning/, /^Ready For Dev/, /^In Progress/, /^Review/, /^Done/])
})
