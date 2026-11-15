import { afterEach, describe, expect, test, vi } from 'vitest'
import { createLinkHandler, isOpenableLink, openLink } from './terminalLinks'

afterEach(() => vi.restoreAllMocks())

const click = (init: MouseEventInit) => new MouseEvent('click', init)
const range = { start: { x: 1, y: 1 }, end: { x: 10, y: 1 } }

function handler(shown: string) {
  const confirm = vi.fn()
  const h = createLinkHandler({ textOf: () => shown, confirm })
  return { h, confirm }
}

describe('terminal links', () => {
  test('only http(s) addresses are openable', () => {
    expect(isOpenableLink('https://example.com/a?b=1')).toBe(true)
    expect(isOpenableLink('http://example.com')).toBe(true)
    for (const bad of ['javascript:alert(1)', 'data:text/html,x', 'file:///etc/passwd', 'vbscript:x', 'ftp://x', '//example.com', 'example.com', '']) {
      expect(isOpenableLink(bad), bad).toBe(false)
    }
  })

  test('opens only with Ctrl or Cmd held, without opener and referrer, when the text shown is the address', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const { h, confirm } = handler('https://example.com')
    h.activate(click({}), 'https://example.com', range)
    expect(open).not.toHaveBeenCalled()
    h.activate(click({ ctrlKey: true }), 'https://example.com', range)
    expect(open).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer')
    const other = handler(' http://example.com ')
    other.h.activate(click({ metaKey: true }), 'http://example.com', range)
    expect(open).toHaveBeenCalledTimes(2)
    expect(confirm).not.toHaveBeenCalled()
  })

  test('a link whose text differs from its address is not opened but shown for confirmation', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const { h, confirm } = handler('https://bank.example')
    h.activate(click({ ctrlKey: true }), 'https://evil.example/login', range)
    expect(open).not.toHaveBeenCalled()
    expect(confirm).toHaveBeenCalledWith('https://evil.example/login', 'https://bank.example')
    openLink('https://evil.example/login')
    expect(open).toHaveBeenCalledWith('https://evil.example/login', '_blank', 'noopener,noreferrer')
  })

  test('javascript:, data: and file: never open or ask, even with Ctrl', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const { h, confirm } = handler('click me')
    for (const url of ['javascript:alert(1)', 'data:text/html,x', 'file:///etc/passwd']) {
      h.activate(click({ ctrlKey: true }), url, range)
    }
    expect(open).not.toHaveBeenCalled()
    expect(confirm).not.toHaveBeenCalled()
    expect(h.allowNonHttpProtocols).toBe(false)
    openLink('javascript:alert(1)')
    expect(open).not.toHaveBeenCalled()
  })
})
