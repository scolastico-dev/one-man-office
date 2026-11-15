import { nextTick, readonly, ref } from 'vue'

const politeText = ref('')
const assertiveText = ref('')

function announce(target: typeof politeText, text: string) {
  // Clear first so the same sentence twice in a row is announced twice.
  target.value = ''
  void nextTick(() => {
    target.value = text
  })
}

/** What the live regions currently say; rendered by LiveAnnouncer. */
export const announcements = { polite: readonly(politeText), assertive: readonly(assertiveText) }

export function useAnnouncer() {
  return {
    polite: (text: string) => announce(politeText, text),
    assertive: (text: string) => announce(assertiveText, text),
  }
}
