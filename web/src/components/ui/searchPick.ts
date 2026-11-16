// The filter of SearchPicker, apart from drawing.

export interface PickItem {
  id: string
  label: string
  /** A second, quieter line (a folder); searched like the label. */
  detail?: string
  /** How the detail is shown when it should be shortened (the middle of a long folder); searched is `detail`. */
  detailShown?: string
  /** Shown whatever the search says (an entry such as "Home folder"). */
  always?: boolean
  /** Disabled items stay reachable so their hint (the reason) can be read. */
  disabled?: boolean
  hint?: string
}

/** The items that match every word of `query` in their label or detail, ignoring case; `always` items stay. */
export function filterPicks<T extends PickItem>(items: readonly T[], query: string): T[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean)
  if (words.length === 0) return [...items]
  return items.filter((i) => {
    if (i.always) return true
    const text = `${i.label}\n${i.detail ?? ''}`.toLowerCase()
    return words.every((w) => text.includes(w))
  })
}
