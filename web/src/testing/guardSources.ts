import { sourceInventory } from './sourceInventory'

/** Enumerate during collection so each source can have its own completed rule check. */
export function guardSources(dir: string): string[] {
  return sourceInventory(dir)
}
