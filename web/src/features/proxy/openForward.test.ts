import { afterEach, expect, test, vi } from 'vitest'
import { createClient, type ApiClient } from '../../api/client'
import { openForward, openingForwards } from './openForward'

const result = (url = 'https://dev.example/_omo/proxy/bootstrap#one-use') => ({ data: { url }, eventId: null })
function setup() {
  const tab = { opener: window, closed: false, location: { href: 'about:blank', replace: vi.fn() }, close: vi.fn() }
  const reserve = vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
  const send = vi.fn().mockResolvedValue(result())
  return { tab, reserve, send, client: { send } as unknown as ApiClient }
}
afterEach(() => { vi.restoreAllMocks(); openingForwards.clear() })

test('reserves exactly one new tab during activation, detaches opener, then navigates the server URL unchanged', async () => {
  const { tab, reserve, send, client } = setup()
  let resolve!: (v: ReturnType<typeof result>) => void
  send.mockReturnValue(new Promise((r) => { resolve = r }))
  const pending = openForward(client, 'abc')
  expect(reserve).toHaveBeenCalledExactlyOnceWith('about:blank', '_blank')
  expect(tab.opener).toBeNull()
  expect(openingForwards.has('abc')).toBe(true)
  await openForward(client, 'abc')
  expect(reserve).toHaveBeenCalledTimes(1)
  expect(send).toHaveBeenCalledExactlyOnceWith('POST', '/api/proxy/forwards/abc/open', {})
  resolve(result())
  await pending
  expect(tab.location.replace).toHaveBeenCalledExactlyOnceWith(result().data.url)
  expect(openingForwards.has('abc')).toBe(false)
})

test('blocked tabs give a useful error without issuing a grant', async () => {
  const { reserve, send, client } = setup()
  reserve.mockReturnValue(null)
  await expect(openForward(client, 'abc')).rejects.toThrow(/allow.*tabs/i)
  expect(send).not.toHaveBeenCalled()
  expect(openingForwards.size).toBe(0)
})

test.each(['Forbidden', 'Cannot reach omo'])('API failure %s closes the reserved blank tab and permits a retry', async (message) => {
  const { tab, send, client } = setup()
  send.mockRejectedValueOnce(new Error(message))
  await expect(openForward(client, 'abc')).rejects.toThrow(message)
  expect(tab.close).toHaveBeenCalledTimes(1)
  expect(openingForwards.size).toBe(0)
  await openForward(client, 'abc')
  expect(tab.location.replace).toHaveBeenCalledTimes(1)
})

test('a reservation closed by the user is reported without creating another tab', async () => {
  const { tab, reserve, send, client } = setup()
  send.mockImplementation(async () => { tab.closed = true; return result() })
  await expect(openForward(client, 'abc')).rejects.toThrow(/closed/i)
  expect(reserve).toHaveBeenCalledTimes(1)
  expect(tab.location.replace).not.toHaveBeenCalled()
})

test('failed navigation cleans up the blank reservation', async () => {
  const { tab, client } = setup()
  tab.location.replace.mockImplementation(() => { throw new Error('navigation refused') })
  await expect(openForward(client, 'abc')).rejects.toThrow('navigation refused')
  expect(tab.close).toHaveBeenCalledTimes(1)
})

test('the board token stays in normal API headers and never enters a destination', async () => {
  const { tab } = setup()
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(result().data)))
  const client = createClient({ getToken: () => 'board-secret', forgetToken: () => null, onUnauthorized: vi.fn(), fetch })
  await openForward(client, 'abc')
  const [url, init] = fetch.mock.calls[0]!
  expect(url).toBe('/api/proxy/forwards/abc/open')
  expect(new Headers(init.headers).get('Authorization')).toBe('Bearer board-secret')
  expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')
  expect(init.body).toBe('{}')
  expect(tab.location.replace.mock.calls[0]![0]).toBe(result().data.url)
  expect(tab.location.replace.mock.calls[0]![0]).not.toContain('board-secret')
})

test('API failure does not close a reservation that the user has navigated elsewhere', async () => {
  const { tab, send, client } = setup()
  send.mockImplementation(async () => { tab.location.href = 'https://user.example/'; throw new Error('Refused') })
  await expect(openForward(client, 'abc')).rejects.toThrow('Refused')
  expect(tab.close).not.toHaveBeenCalled()
})

test('separate successful Opens always create separate unnamed tabs', async () => {
  const { reserve, client } = setup()
  await openForward(client, 'abc')
  await openForward(client, 'abc')
  await openForward(client, 'other')
  expect(reserve.mock.calls).toEqual(Array.from({ length: 3 }, () => ['about:blank', '_blank']))
})
