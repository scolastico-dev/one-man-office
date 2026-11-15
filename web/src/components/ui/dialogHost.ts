import type { InjectionKey } from 'vue'

/** Provide `true` to render dialogs where they are written instead of in the body (unit tests only). */
export const DIALOGS_IN_PLACE: InjectionKey<boolean> = Symbol('dialogs-in-place')
