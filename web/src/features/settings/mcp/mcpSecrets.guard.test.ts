// Guards of the MCP pages and the consent page: values (env, headers, the
// OAuth client secret, a new token's secret) never go into a toast, the
// browser's console or an address omo builds, and no text offers the
// blocking `wait` command as a tool: it is command line only.
import { expect, test } from 'vitest'

const sources = Object.fromEntries(
  Object.entries(
    import.meta.glob(['/src/features/settings/mcp/*.{vue,ts}', '/src/features/oauth/*.{vue,ts}'], {
      query: '?raw',
      import: 'default',
      eager: true,
    }) as Record<string, string>,
  ).filter(([file]) => !file.endsWith('.test.ts')),
)

test('the guard sees the files it scans', () => {
  expect(Object.keys(sources)).toEqual(expect.arrayContaining(['/src/features/settings/mcp/McpAccess.vue', '/src/features/settings/mcp/McpServerEditor.vue', '/src/features/oauth/OAuthConsent.vue']))
})

test('no MCP page shows a toast or writes to the console', () => {
  const found = Object.entries(sources)
    .filter(([, text]) => /\buseToast\b|\bconsole\.[a-z]+\(/.test(text))
    .map(([file]) => file)
  expect(found).toEqual([])
})

/** Template strings that build an address from a value, a secret or a token. */
function addressesFromValues(text: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(/`[^`\n]*\$\{[^}]*(secret|token|env|headers|client_secret|clientSecret)[^}]*\}[^`\n]*`/gi)) {
    if (/https?:|\?|&[a-z_]+=/.test(m[0])) out.push(m[0])
  }
  return out
}

test('the guard recognises an address built from a secret', () => {
  expect(addressesFromValues('const u = `https://x.invalid/cb?t=${secret.value}`')).toHaveLength(1)
  expect(addressesFromValues('const t = `Token ${name.value} created.`')).toHaveLength(0)
})

test('no MCP page builds an address from a value, a secret or a token', () => {
  const found = Object.entries(sources).flatMap(([file, text]) => addressesFromValues(text).map((m) => `${file}: ${m}`))
  expect(found).toEqual([])
})

test('no MCP text offers wait as a tool', () => {
  const found = Object.entries(sources)
    .filter(([, text]) => /\bwait\b/.test(text))
    .map(([file]) => file)
  expect(found).toEqual([])
})

// structuredClone throws in browsers on Vue's reactive proxies (props, refs of
// objects), while happy-dom lets it pass, so unit tests do not see it: app
// code copies plain data with JSON or spreads instead.
const appSources = import.meta.glob(['/src/**/*.{vue,ts}'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

test('no app code clones with structuredClone', () => {
  const found = Object.entries(appSources)
    .filter(([file]) => !file.endsWith('.test.ts') && !/[/T]esting\.ts$/.test(file) && !file.startsWith('/src/testing/'))
    .filter(([, text]) => /\bstructuredClone\(/.test(text))
    .map(([file]) => file)
  expect(found).toEqual([])
})
