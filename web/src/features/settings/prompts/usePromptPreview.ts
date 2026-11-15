// The preview of a prompt draft, asked for while the user types. Requests are
// debounced and numbered: an answer is shown only when it belongs to the
// newest text asked for, so a slow answer for older text never replaces the
// preview of newer text.
import { onScopeDispose, readonly, ref, getCurrentScope, type Ref } from 'vue'
import type { PromptPreview } from '../../../api/types'

export interface PromptPreviewOptions {
  /** Asks the server for the preview of `text` as prompt `name`. */
  request(name: string, text: string): Promise<PromptPreview>
  /** Milliseconds of quiet before a request is sent; 300 by default. */
  delay?: number
}

export interface PromptPreviewState {
  /** The rendered prompt; empty while the template has an error or before the first answer. */
  text: Readonly<Ref<string>>
  /** The template's error as the server words it; empty when there is none. */
  error: Readonly<Ref<string>>
  /** The preview could not be asked for (not a template error). */
  failure: Readonly<Ref<string>>
  /** An answer for the newest text is on its way or due. */
  busy: Readonly<Ref<boolean>>
  /** True once an answer for some text was shown. */
  ready: Readonly<Ref<boolean>>
  /** Asks after the delay, unless the text changes again first. */
  schedule(name: string, text: string): void
  /** Asks now. */
  now(name: string, text: string): void
  /** Forgets everything under way; no answer is shown afterwards. */
  stop(): void
}

export function createPromptPreview(o: PromptPreviewOptions): PromptPreviewState {
  const delay = o.delay ?? 300
  const text = ref('')
  const error = ref('')
  const failure = ref('')
  const busy = ref(false)
  const ready = ref(false)
  let timer: ReturnType<typeof setTimeout> | null = null
  /** Counts every text asked for; only the answer of the last one is shown. */
  let seq = 0

  function clearTimer() {
    if (timer !== null) clearTimeout(timer)
    timer = null
  }

  async function run(name: string, draft: string, mine: number) {
    try {
      const got = await o.request(name, draft)
      if (mine !== seq) return
      text.value = got.text
      error.value = got.error
      failure.value = ''
      ready.value = true
    } catch (e) {
      if (mine !== seq) return
      failure.value = e instanceof Error && e.message !== '' ? e.message : 'The preview could not be made.'
    } finally {
      if (mine === seq) busy.value = false
    }
  }

  function schedule(name: string, draft: string) {
    clearTimer()
    const mine = ++seq
    busy.value = true
    timer = setTimeout(() => {
      timer = null
      void run(name, draft, mine)
    }, delay)
  }

  function now(name: string, draft: string) {
    clearTimer()
    const mine = ++seq
    busy.value = true
    void run(name, draft, mine)
  }

  function stop() {
    clearTimer()
    seq++
    busy.value = false
  }

  return { text: readonly(text), error: readonly(error), failure: readonly(failure), busy: readonly(busy), ready: readonly(ready), schedule, now, stop }
}

/** `createPromptPreview` that stops with the calling component. */
export function usePromptPreview(o: PromptPreviewOptions): PromptPreviewState {
  const p = createPromptPreview(o)
  if (getCurrentScope()) onScopeDispose(p.stop)
  return p
}
