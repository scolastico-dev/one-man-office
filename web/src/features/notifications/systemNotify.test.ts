import { expect, test } from 'vitest'
import { MemoryStorage } from '../../testing/fakeOmo'
import { BODY_MAX, createSystemNotifier, DESKTOP_KEY, TITLE_MAX, type NotificationCtor, type SystemItem } from './systemNotify'

interface Shown {
  title: string
  body: string | undefined
  tag: string | undefined
  closed: boolean
  click(): void
}

function fakeApi(permission: NotificationPermission, answer: NotificationPermission = permission) {
  const shown: Shown[] = []
  let asked = 0
  class FakeNotification {
    static permission: NotificationPermission = permission
    static requestPermission(): Promise<NotificationPermission> {
      asked++
      FakeNotification.permission = answer
      return Promise.resolve(answer)
    }
    onclick: ((this: unknown, ev: Event) => unknown) | null = null
    private entry: Shown
    constructor(title: string, opts?: NotificationOptions) {
      this.entry = { title, body: opts?.body, tag: opts?.tag, closed: false, click: () => this.onclick?.call(this, new Event('click')) }
      shown.push(this.entry)
    }
    close() {
      this.entry.closed = true
    }
  }
  return { api: FakeNotification as unknown as NotificationCtor, shown, asked: () => asked, set: (p: NotificationPermission) => (FakeNotification.permission = p) }
}

function setup(o: { permission?: NotificationPermission; answer?: NotificationPermission; stored?: string; initial?: string[]; missing?: boolean } = {}) {
  const fake = fakeApi(o.permission ?? 'default', o.answer)
  const storage = new MemoryStorage()
  if (o.stored !== undefined) storage.setItem(DESKTOP_KEY, o.stored)
  const doc = { visibilityState: 'hidden' as DocumentVisibilityState }
  const clicked: SystemItem[] = []
  let focused = 0
  const n = createSystemNotifier({
    Notification: o.missing ? undefined : fake.api,
    document: doc,
    storage: () => storage,
    onClick: (item) => clicked.push(item),
    focus: () => focused++,
    ...(o.initial ? { initial: o.initial } : {}),
  })
  return { n, fake, storage, doc, clicked, focused: () => focused }
}

const item = (key: string, title = `Title ${key}`, body = `Body ${key}`): SystemItem => ({ key, title, body })

test('desktop notifications: off by default; only after enable and permission; never for initial items; only while hidden; tag per item', async () => {
  const f = setup({ permission: 'default', answer: 'granted', initial: ['q:1'] })
  expect(f.n.supported).toBe(true)
  expect(f.n.enabled.value).toBe(false)
  // Off: nothing shows, and nobody asked the browser.
  expect(f.n.notify(item('q:2'))).toBe(false)
  expect(f.fake.asked()).toBe(0)

  expect(await f.n.enable()).toBe(true)
  expect(f.fake.asked()).toBe(1)
  expect(f.n.enabled.value).toBe(true)
  expect(f.storage.getItem(DESKTOP_KEY)).toBe('on')

  // Present at start: never.
  expect(f.n.notify(item('q:1'))).toBe(false)
  // Visible page: the bell is enough, and the item is not shown later either.
  f.doc.visibilityState = 'visible'
  expect(f.n.notify(item('q:3'))).toBe(false)
  f.doc.visibilityState = 'hidden'
  expect(f.n.notify(item('q:3'))).toBe(false)
  // Hidden page, new item: once, tagged with its key.
  expect(f.n.notify(item('q:4'))).toBe(true)
  expect(f.n.notify(item('q:4'))).toBe(false)
  expect(f.fake.shown.map((s) => [s.title, s.tag])).toEqual([['Title q:4', 'q:4']])

  // A click focuses the tab, opens the item and closes the notification.
  f.fake.shown[0]!.click()
  expect(f.focused()).toBe(1)
  expect(f.clicked.map((c) => c.key)).toEqual(['q:4'])
  expect(f.fake.shown[0]!.closed).toBe(true)

  f.n.disable()
  expect(f.n.enabled.value).toBe(false)
  expect(f.storage.getItem(DESKTOP_KEY)).toBe('off')
  expect(f.n.notify(item('q:5'))).toBe(false)
})

test('denied permission keeps them off and says why; a dismissed prompt too', async () => {
  const denied = setup({ permission: 'default', answer: 'denied' })
  expect(await denied.n.enable()).toBe(false)
  expect(denied.n.enabled.value).toBe(false)
  expect(denied.n.reason.value).toMatch(/blocked/)
  expect(denied.storage.getItem(DESKTOP_KEY)).not.toBe('on')

  const dismissed = setup({ permission: 'default', answer: 'default' })
  expect(await dismissed.n.enable()).toBe(false)
  expect(dismissed.n.reason.value).not.toBe('')
})

test('a stored choice counts only while the browser still grants permission', () => {
  expect(setup({ permission: 'granted', stored: 'on' }).n.enabled.value).toBe(true)
  expect(setup({ permission: 'denied', stored: 'on' }).n.enabled.value).toBe(false)
  expect(setup({ permission: 'granted' }).n.enabled.value).toBe(false)
  const f = setup({ permission: 'granted', stored: 'on' })
  // Revoked in the site settings later: silently nothing.
  f.fake.set('denied')
  expect(f.n.notify(item('x'))).toBe(false)
  expect(f.fake.shown).toEqual([])
})

test('without the Notification API everything is off and nothing throws', async () => {
  const f = setup({ missing: true, stored: 'on' })
  expect(f.n.supported).toBe(false)
  expect(f.n.enabled.value).toBe(false)
  expect(await f.n.enable()).toBe(false)
  expect(f.n.notify(item('x'))).toBe(false)
})

test('a browser that throws on new Notification degrades silently', () => {
  const storage = new MemoryStorage()
  storage.setItem(DESKTOP_KEY, 'on')
  const Throwing = function () {
    throw new TypeError('Illegal constructor')
  } as unknown as NotificationCtor
  Object.assign(Throwing, { permission: 'granted', requestPermission: () => Promise.resolve('granted') })
  const n = createSystemNotifier({ Notification: Throwing, document: { visibilityState: 'hidden' }, storage: () => storage, onClick: () => {} })
  expect(n.notify(item('x'))).toBe(false)
})

test('title and body are plain, single-line, visible and capped', async () => {
  const f = setup({ permission: 'granted', stored: 'on' })
  const long = 'a'.repeat(500)
  f.n.notify({ key: 'k', title: `evil‮txt.exe\n<b>${long}`, body: `line one\n\nline\ttwo ${long}` })
  const s = f.fake.shown[0]!
  expect(s.title.length).toBeLessThanOrEqual(TITLE_MAX)
  expect(s.body!.length).toBeLessThanOrEqual(BODY_MAX)
  expect(s.title).toContain('\\u{202E}')
  expect(s.title).toContain('<b>')
  expect(s.title.endsWith('…')).toBe(true)
  expect(s.body!.startsWith('line one line two ')).toBe(true)
})

test('storage that throws is the same as no stored choice', async () => {
  const fake = fakeApi('default', 'granted')
  const broken = {
    getItem: () => {
      throw new Error('blocked')
    },
    setItem: () => {
      throw new Error('blocked')
    },
  } as unknown as Storage
  const n = createSystemNotifier({ Notification: fake.api, document: { visibilityState: 'hidden' }, storage: () => broken, onClick: () => {} })
  expect(n.enabled.value).toBe(false)
  expect(await n.enable()).toBe(true)
  expect(n.enabled.value).toBe(true)
})
