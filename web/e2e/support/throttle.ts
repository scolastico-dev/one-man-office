import type { BrowserContext, CDPSession, Page } from '@playwright/test'

export function cpuThrottleRate(value: string | undefined): number {
  const rate = value === undefined ? 1 : Number(value)
  if (value?.trim() === '' || !Number.isFinite(rate) || rate < 1) {
    throw new Error('E2E_CPU_THROTTLE_RATE must be a finite number >= 1')
  }
  return rate
}

/** Throttle newPage before return and popups before their script resources. */
export async function installCPUThrottle(context: BrowserContext, rate: number): Promise<() => Promise<void>> {
  if (!Number.isFinite(rate) || rate < 1) throw new Error('cpuThrottleRate must be a finite number >= 1')
  if (rate === 1) return async () => {}
  const sessions: { page: Page; cdp: CDPSession }[] = []
  const detachSessions = () => Promise.all(sessions.map(async ({ page, cdp }) => {
    try {
      await cdp.detach()
    } catch (e) {
      if (!page.isClosed()) throw e
    }
  }))
  const pending = new Map<Page, Promise<void>>()
  const apply = (page: Page) => {
    let task = pending.get(page)
    if (!task) {
      task = (async () => {
        try {
          const cdp = await context.newCDPSession(page)
          sessions.push({ page, cdp })
          await cdp.send('Emulation.setCPUThrottlingRate', { rate })
        } catch (e) {
          if (!page.isClosed()) throw e
        }
      })()
      pending.set(page, task)
    }
    return task
  }
  const newPage = context.newPage
  context.newPage = async () => {
    const page = await newPage.call(context)
    await apply(page)
    return page
  }
  // Playwright has no frame for a popup's initial document request. The page
  // event applies the rate as soon as it exists, and resource routing holds
  // the app's external scripts until CDP acknowledges it. Inline scripts in
  // another app's initial popup document are outside this hook's guarantee.
  const onPage = (page: Page) => { void apply(page).catch(() => {}) }
  context.on('page', onPage)
  const route: Parameters<BrowserContext['route']>[1] = async (route) => {
    const request = route.request()
    let page: Page | undefined
    try {
      page = request.frame().page()
    } catch (e) {
      if (!request.isNavigationRequest() || !(e instanceof Error) || !e.message.includes('Frame for this navigation request is not available')) throw e
    }
    if (page) await apply(page)
    await route.fallback()
  }
  try {
    await context.route('**/*', route)
    await Promise.all(context.pages().map(apply))
  } catch (e) {
    context.newPage = newPage
    context.off('page', onPage)
    await context.unroute('**/*', route)
    await detachSessions()
    throw e
  }
  return async () => {
    context.newPage = newPage
    context.off('page', onPage)
    await context.unroute('**/*', route)
    // Observe background popup failures as well, even if it fetched no resources.
    try {
      await Promise.all(pending.values())
    } finally {
      await detachSessions()
    }
  }
}
