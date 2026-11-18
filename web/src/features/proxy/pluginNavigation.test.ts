import { afterEach, expect, test, vi } from 'vitest'
import type { ApiClient } from '../../api/client'
import { navigatePluginResult } from './pluginNavigation'

afterEach(() => vi.restoreAllMocks())
function setup() {
  const tab = { opener: window, closed: false, location: { href: 'about:blank', replace: vi.fn() }, close: vi.fn() }
  const reserve = vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
  const send = vi.fn().mockResolvedValue({ data: { url: 'https://forward.example/_omo/proxy/bootstrap#grant' } })
  return { tab, reserve, send, client: { send } as unknown as ApiClient }
}

test('reserves before any hook/polling and only acquires a grant after the final result', async () => {
  const { client, tab, send, reserve } = setup()
  let finish!: (value: { forward_id: string; message: string }) => void
  const action = vi.fn(() => new Promise<{ forward_id: string; message: string }>((r) => { finish = r }))
  const pending = navigatePluginResult(client, action)
  expect(reserve).toHaveBeenCalledTimes(1)
  expect(tab.opener).toBeNull()
  expect(action).toHaveBeenCalledTimes(1)
  expect(send).not.toHaveBeenCalled()
  finish({ forward_id: '0123456789abcdef0123456789abcdef', message: 'Ready' })
  expect(await pending).toEqual({ forward_id: '0123456789abcdef0123456789abcdef', message: 'Ready' })
  expect(send).toHaveBeenCalledExactlyOnceWith('POST', '/api/proxy/forwards/0123456789abcdef0123456789abcdef/open', {})
  expect(tab.location.replace).toHaveBeenCalledExactlyOnceWith('https://forward.example/_omo/proxy/bootstrap#grant')
})

test('message-only answers and lost hook answers close the unused tab without rerunning', async () => {
  const { client, tab } = setup()
  await navigatePluginResult(client, async () => ({ message: 'Done' }))
  expect(tab.close).toHaveBeenCalledTimes(1)
  const action = vi.fn().mockRejectedValue(new TypeError('network lost'))
  await expect(navigatePluginResult(client, action)).rejects.toThrow('network lost')
  expect(action).toHaveBeenCalledTimes(1)
  expect(tab.close).toHaveBeenCalledTimes(2)
})

test.each(['javascript:alert(1)', 'https://user:pass@example.test/', 'https://example.test/\\evil'])('refuses unsafe destination %s', async (url) => {
  const { client, tab } = setup()
  await expect(navigatePluginResult(client, async () => ({ message: '', url }))).rejects.toThrow(/address/i)
  expect(tab.location.replace).not.toHaveBeenCalled()
  expect(tab.close).toHaveBeenCalledTimes(1)
})

test('keeps a safe URL byte-for-byte and does not close a user-navigated reservation on failure', async () => {
  const { client, tab } = setup()
  await navigatePluginResult(client, async () => ({ message: '', url: 'https://example.test/a%2Fb?q=%2F#L2' }))
  expect(tab.location.replace).toHaveBeenCalledWith('https://example.test/a%2Fb?q=%2F#L2')
  tab.location.href = 'https://user.example/'
  await expect(navigatePluginResult(client, async () => { throw new Error('failed') })).rejects.toThrow('failed')
  expect(tab.close).not.toHaveBeenCalled()
})

test('blocked popups still permit message-only hooks while refusing navigation without a duplicate hook', async () => {
  const { client, reserve } = setup()
  reserve.mockReturnValue(null)
  expect(await navigatePluginResult(client, async () => ({ message: 'done' }))).toEqual({ message: 'done' })
  const action = vi.fn().mockResolvedValue({ message: '', url: 'https://example.test/' })
  await expect(navigatePluginResult(client, action)).rejects.toThrow(/tabs/i)
  expect(action).toHaveBeenCalledTimes(1)
})
