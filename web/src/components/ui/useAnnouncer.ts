import { nextTick, readonly, ref, type Ref } from 'vue'

/** Sentences said together, each ending like a sentence so a screen reader pauses between them. */
function joined(parts: readonly string[]): string {
  if (parts.length === 1) return parts[0]!
  return parts.map((p) => (/[.!?…:]$/.test(p) ? p : `${p}.`)).join(' ')
}

/**
 * One live region. Everything said in one tick is joined into one text, so
 * no sentence replaces another before it was heard; a sentence said twice in
 * that tick is said once.
 */
function channel(): { text: Ref<string>; say(s: string): void } {
  const text = ref('')
  let parts: string[] = []
  return {
    text,
    say(s) {
      if (parts.includes(s)) return
      parts.push(s)
      if (parts.length > 1) return
      // Clear first so the same sentence twice in a row is announced twice.
      text.value = ''
      void nextTick(() => {
        text.value = joined(parts)
        parts = []
      })
    },
  }
}

const polite = channel()
const assertive = channel()

/** What the live regions currently say; rendered by LiveAnnouncer. */
export const announcements = { polite: readonly(polite.text), assertive: readonly(assertive.text) }

export function useAnnouncer() {
  return {
    polite: (text: string) => polite.say(text),
    assertive: (text: string) => assertive.say(text),
  }
}
