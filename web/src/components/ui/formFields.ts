// What a form tells the fields inside it, and what they tell the form. A
// field whose text is not a value yet (a number field holding "4x") reports
// it, so the form can block its save; when the form drops its edits
// (Discard, a new stored value) it counts `resets` up and the fields show
// the value again, also where the value itself did not change.
import type { InjectionKey, Ref } from 'vue'

export interface FormFields {
  /** A field (by a key unique in the form) holds text that is no value (`message`), or a value again (`null`). */
  setParse(field: string, message: string | null): void
  /** Counts up whenever the form drops its edits. */
  resets: Readonly<Ref<number>>
}

export const FORM_FIELDS: InjectionKey<FormFields> = Symbol('form-fields')
