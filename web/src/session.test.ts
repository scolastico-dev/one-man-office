import { flushPromises } from '@vue/test-utils'
import { expect, test, vi } from 'vitest'
import { createTokenStore } from './auth/token'
import { createSession } from './session'
import { FakeOmo, MemoryStorage } from './testing/fakeOmo'

const TOKEN = 'a'.repeat(64)
const OTHER = 'b'.repeat(64)

function setup(omo: FakeOmo, stored?: string) {
  const storage = new MemoryStorage()
  if (stored) storage.setItem('omo.token', stored)
  const tokens = createTokenStore(() => storage)
  const session = createSession({ tokens, makeClient: omo.makeClient, connect: omo.connect })
  const replaceState = vi.fn()
  const hist = { state: null, replaceState } as unknown as History
  const at = (hash: string) => ({ hash, pathname: '/', search: '' }) as Location
  return { session, storage, hist, replaceState, at }
}

test('a token that arrives through a fragment change unlocks the locked screen and leaves the address', async () => {
  const omo = new FakeOmo()
  omo.token = TOKEN
  const { session, storage, hist, replaceState, at } = setup(omo)
  session.start()
  await flushPromises()
  expect(session.phase.value).toBe('locked')

  session.addressChanged(at('#' + TOKEN), hist)
  await flushPromises()

  expect(storage.getItem('omo.token')).toBe(TOKEN)
  expect(replaceState).toHaveBeenCalledWith(null, '', '/')
  expect(session.phase.value).toBe('ready')
  session.store.stop()
})

test('a fragment that is not a token is left alone', async () => {
  const omo = new FakeOmo()
  omo.token = TOKEN
  const { session, storage, hist, replaceState, at } = setup(omo)
  session.start()
  await flushPromises()

  session.addressChanged(at('#section'), hist)
  await flushPromises()

  expect(replaceState).not.toHaveBeenCalled()
  expect(storage.getItem('omo.token')).toBeNull()
  expect(session.phase.value).toBe('locked')
})

test('on a shown board a different valid token replaces the stored one and the board stays', async () => {
  const omo = new FakeOmo()
  omo.token = OTHER
  const { session, storage, hist, replaceState, at } = setup(omo, TOKEN)
  omo.token = TOKEN
  session.start()
  await flushPromises()
  expect(session.phase.value).toBe('ready')
  omo.token = OTHER // rotated while the page was open

  session.addressChanged(at('#' + OTHER), hist)
  await flushPromises()

  expect(storage.getItem('omo.token')).toBe(OTHER)
  expect(replaceState).toHaveBeenCalledTimes(1)
  expect(session.phase.value).toBe('ready')
  session.store.stop()
})

test('on a shown board a refused token from the address is handled by the existing rules', async () => {
  const omo = new FakeOmo()
  omo.token = TOKEN
  const { session, storage, hist, at } = setup(omo, TOKEN)
  session.start()
  await flushPromises()
  expect(session.phase.value).toBe('ready')

  // A stale address: the server still wants the token this page had.
  session.addressChanged(at('#' + OTHER), hist)
  await flushPromises()
  expect(storage.getItem('omo.token')).toBe(TOKEN)
  expect(session.phase.value).toBe('ready')

  // After the server stops accepting every known token the screen locks.
  omo.token = 'c'.repeat(64)
  session.addressChanged(at('#' + OTHER), hist)
  await flushPromises()
  expect(session.phase.value).toBe('locked')
  expect(session.lockReason.value).toBe('rejected')
  session.store.stop()
})
