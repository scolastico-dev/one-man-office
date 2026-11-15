import { expect, test } from 'vitest'

// Every source file of the app except tests, as text.
const sources = import.meta.glob(['/src/**/*.{vue,ts}', '!/src/**/*.test.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

/**
 * Files that may write a board, ticket or settings address by hand: the helper itself,
 * the router's table, and the Markdown renderer, whose links carry `/<n>` only
 * for opening in a new tab (a click on them goes through the helper).
 */
const ALLOWED = new Set(['/src/locations.ts', '/src/router.ts', '/src/features/ticket/markdown.ts'])

const PATTERNS: RegExp[] = [
  // `/${n}`, '/12', '/?q=': an app address written as a string.
  /`\/\$\{/g,
  /[`'"]\/(?:\d|\?)/g,
  // { path: '/' } or { path: `/${n}` } for a navigation.
  /\bpath:\s*[`'"]\/(?:\$\{|[`'"?])/g,
  // router.push('/…') or router.push(path): a navigation to a string.
  /\brouter\.(?:push|replace)\(\s*(?:[`'"]|\w+\s*\))/g,
  // <RouterLink :to="`/…`"> or :to="{ path: … }".
  /:to="\s*(?:[`'"]|\{\s*path)/g,
  // '/settings', `/settings/${section}`, '/settings/limits?q=x': a settings address written as a string.
  /[`'"]\/settings\b/g,
]

/** Test helpers set up their own routers at fixed addresses. */
const isTestingHelper = (file: string) => file.startsWith('/src/testing/') || file.startsWith('/src/sample/') || /[/T]esting\.ts$|\/testing\.ts$/.test(file)

function handBuilt(file: string, text: string): string[] {
  const found: string[] = []
  for (const re of PATTERNS) for (const m of text.matchAll(re)) found.push(`${file}: ${text.slice(Math.max(0, m.index! - 30), m.index! + 40).replace(/\s+/g, ' ')}`)
  return found
}

test('the guard sees the files it scans', () => {
  expect(Object.keys(sources).length).toBeGreaterThan(50)
  expect(sources['/src/features/board/LiveBoard.vue']).toBeDefined()
})

test('the guard recognises a hand-built ticket address', () => {
  expect(handBuilt('x.vue', '<RouterLink :to="`/${t.number}`">')).toHaveLength(2)
  expect(handBuilt('x.ts', "if (e.key !== '/') return")).toEqual([])
  expect(handBuilt('x.ts', "router.push({ path: `/${n}`, query: route.query })")).toHaveLength(2)
  expect(handBuilt('x.ts', "void router.replace({ path: '/', query })")).toHaveLength(1)
  expect(handBuilt('x.ts', 'void router.push(path)')).toHaveLength(1)
  expect(handBuilt('x.vue', '<a :href="`/${t.number}`">')).toHaveLength(1)
  expect(handBuilt('x.ts', "return `/${item.ticket}?tab=comments`")).toHaveLength(1)
  expect(handBuilt('x.ts', "router.push(ticketLocation(n, route, { tab: 'diff' }))")).toEqual([])
  expect(handBuilt('x.ts', "client.get(`/api/tickets/${n}`)")).toEqual([])
})

test('the guard recognises a hand-built settings address', () => {
  expect(handBuilt('x.ts', "router.push('/settings/limits')")).toHaveLength(2)
  expect(handBuilt('x.ts', "router.push('/settings/plugins')")).toHaveLength(2)
  expect(handBuilt('x.ts', 'router.push({ path: `/settings/${id}`, query: route.query })')).toHaveLength(1)
  expect(handBuilt('x.vue', '<RouterLink to="/settings">')).toHaveLength(1)
  expect(handBuilt('x.vue', `<RouterLink :to="'/settings'">`)).toHaveLength(2)
  expect(handBuilt('x.ts', "const back = '/settings'")).toHaveLength(1)
  expect(handBuilt('x.ts', "router.push(settingsLocation('limits', route))")).toEqual([])
  expect(handBuilt('x.ts', "client.get('/api/settings')")).toEqual([])
  expect(handBuilt('x.ts', "c.send('PATCH', '/api/settings', patch)")).toEqual([])
})

test('board, ticket and settings addresses are built with ticketLocation, boardLocation and settingsLocation only', () => {
  const found: string[] = []
  for (const [file, text] of Object.entries(sources)) {
    if (ALLOWED.has(file) || isTestingHelper(file)) continue
    found.push(...handBuilt(file, text))
  }
  expect(found).toEqual([])
})
