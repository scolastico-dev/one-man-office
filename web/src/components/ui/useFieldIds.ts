import { computed, useId, type Ref } from 'vue'

/** Ids for a labelled form control and the texts that describe it. */
export function useFieldIds(props: { id?: string; hint?: string; error?: string }) {
  const base = useId()
  const id = computed(() => props.id ?? `${base}-control`)
  const hintId = `${base}-hint`
  const errorId = `${base}-error`
  const describedBy: Ref<string | undefined> = computed(() => {
    const ids = [props.hint ? hintId : '', props.error ? errorId : ''].filter(Boolean)
    return ids.length ? ids.join(' ') : undefined
  })
  return { id, hintId, errorId, describedBy }
}
