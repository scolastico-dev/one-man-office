import { expect, test } from 'vitest'
import { nextTick } from 'vue'
import { announcements, useAnnouncer } from './useAnnouncer'

const a = useAnnouncer()

test('everything said in one tick is heard, in order, as one announcement', async () => {
  a.polite('New comment from developer Ada.')
  a.polite('New comment from reviewer Bo.')
  a.polite('#4 waits for your review')
  await nextTick()
  expect(announcements.polite.value).toBe('New comment from developer Ada. New comment from reviewer Bo. #4 waits for your review.')
})

test('polite and assertive stay apart', async () => {
  a.polite('Saved')
  a.assertive('The server refused it')
  await nextTick()
  expect(announcements.polite.value).toBe('Saved')
  expect(announcements.assertive.value).toBe('The server refused it')
})

test('the same sentence twice in one tick is said once; in the next tick it is said again', async () => {
  a.polite('Done.')
  a.polite('Done.')
  await nextTick()
  expect(announcements.polite.value).toBe('Done.')
  a.polite('Done.')
  // Cleared first, so a screen reader hears the same text again.
  expect(announcements.polite.value).toBe('')
  await nextTick()
  expect(announcements.polite.value).toBe('Done.')
})
