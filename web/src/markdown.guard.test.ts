import { expect, test } from 'vitest'

// Every component of the app, as text.
const sources = import.meta.glob(['/src/**/*.vue', '!/src/**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

/**
 * The elements that show rendered Markdown (`v-html`) in a file that has no
 * `md` class on them: their lists, headings, quotes and tables would come out
 * unstyled (Tailwind's reset removes list markers), which `styles/markdown.css`
 * prevents for every element that carries the class.
 */
function unstyled(file: string, text: string): string[] {
  const found: string[] = []
  for (const tag of text.matchAll(/<[a-z][^<>]*\bv-html=[^<>]*>/gi)) {
    if (!/\bclass="[^"]*\bmd\b[^"]*"/.test(tag[0])) found.push(`${file}: ${tag[0].replace(/\s+/g, ' ')}`)
  }
  return found
}

test.each(Object.entries(sources))('rendered Markdown is shown only in an element with the md class: %s', (file, text) => {
  expect(unstyled(file, text), file).toEqual([])
})

const using = Object.entries(sources).filter(([, text]) => /\bv-html=/.test(text)).map(([file]) => file)

test('the guard sees both Markdown renderers', () => {
  // The guard looks at something: both places that render Markdown have the class.
  expect(using.some((f) => f.endsWith('/comments/CommentItem.vue'))).toBe(true)
  expect(using.some((f) => f.endsWith('/ticket/DescriptionView.vue'))).toBe(true)
})

test('the guard finds an element without the class', () => {
  expect(unstyled('x.vue', '<div class="body" v-html="html" />')).toHaveLength(1)
  expect(unstyled('x.vue', '<div v-html="html" />')).toHaveLength(1)
  expect(unstyled('x.vue', '<div class="body md" data-x v-html="html" @click="f" />')).toEqual([])
})
