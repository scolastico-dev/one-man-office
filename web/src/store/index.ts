import { inject, provide, type InjectionKey } from 'vue'
import type { BoardStore } from './board'

export { createBoardStore, CONFIRM_TIMEOUT_MS, type BoardStore, type BoardStoreDeps, type ColumnTickets, type PendingMove } from './board'
export type { BoardState, Entry } from './boardState'

const BOARD: InjectionKey<BoardStore> = Symbol('board')

/** Makes the store available to the components below the caller. */
export function provideBoard(store: BoardStore): void {
  provide(BOARD, store)
}

/** The store provided by an ancestor with `provideBoard`. */
export function useBoard(): BoardStore {
  const store = inject(BOARD, null)
  if (store === null) throw new Error('useBoard() needs provideBoard() in an ancestor component')
  return store
}
