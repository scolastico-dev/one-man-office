import { afterEach, describe, expect, test, vi } from 'vitest'
import { isOpenableLink, linkHandler } from './terminalLinks'

afterEach(() => vi.restoreAllMocks())

const click = (init: MouseEventInit) => new MouseEvent('click', init)
const range = {} as never

describe('terminal links', () => {
  test('only http(s) addresses are openable', () => {
    expect(isOpenableLink('https://example.com/a?b=1')).toBe(true)
    expect(isOpenableLink('http://example.com')).toBe(true)
    for (const bad of ['javascript:alert(1)', 'data:text/html,x', 'file:///etc/passwd', 'vbscript:x', 'ftp://x', '//example.com', 'example.com', '']) {
      expect(isOpenableLink(bad), bad).toBe(false)
    }
  })

  test('opens only with Ctrl or Cmd held, without opener and referrer', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    linkHandler.activate(click({}), 'https://example.com', range)
    expect(open).not.toHaveBeenCalled()
    linkHandler.activate(click({ ctrlKey: true }), 'https://example.com', range)
    linkHandler.activate(click({ metaKey: true }), 'http://example.com', range)
    expect(open).toHaveBeenCalledTimes(2)
    expect(open).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer')
  })

  test('javascript:, data: and file: never open, even with Ctrl', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    for (const url of ['javascript:alert(1)', 'data:text/html,x', 'file:///etc/passwd']) {
      linkHandler.activate(click({ ctrlKey: true }), url, range)
    }
    expect(open).not.toHaveBeenCalled()
    expect(linkHandler.allowNonHttpProtocols).toBe(false)
  })
})
