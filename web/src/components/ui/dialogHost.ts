import type { InjectionKey, Ref } from 'vue'

/** Provide `true` to render dialogs where they are written instead of in the body (unit tests only). */
export const DIALOGS_IN_PLACE: InjectionKey<boolean> = Symbol('dialogs-in-place')

/**
 * Provided by the app: true while the board is kept, hidden and inert, under
 * the lock screen (or while it loads again after an unlock), so drafts survive
 * a lock. Dialogs opened from the board step aside meanwhile (they live in
 * the body, outside the hidden board) and come back after; terminals close
 * their connection; keyboard shortcuts of the board do nothing.
 */
export const BOARD_SUSPENDED: InjectionKey<Readonly<Ref<boolean>>> = Symbol('board-suspended')
