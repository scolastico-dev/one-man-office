// Text being written on a ticket that is not sent yet: the description being
// edited and the comment in the composer. Kept per ticket in this tab's memory
// (never saved), so it survives switching between the ticket's panel and its
// page, and between the panel's tabs. Gone after a reload.
import { reactive } from 'vue'

/** A description edit in progress. */
export interface DescriptionDraft {
  /** The text typed so far. */
  text: string
  /** The version the edit is based on. */
  base: number
  /** The text the edit started from; the draft is unsaved while `text` differs. */
  initial: string
}

const descriptions = reactive(new Map<number, DescriptionDraft>())
const comments = reactive(new Map<number, string>())

export const ticketDrafts = {
  /** The description being edited on ticket #n, if any. */
  description(n: number): DescriptionDraft | undefined {
    return descriptions.get(n)
  },
  /** Keeps (or with `null` forgets) the description being edited on ticket #n. */
  setDescription(n: number, d: DescriptionDraft | null): void {
    if (d === null) descriptions.delete(n)
    else descriptions.set(n, { ...d })
  },
  /** The comment being written on ticket #n; empty when none. */
  comment(n: number): string {
    return comments.get(n) ?? ''
  },
  /** Keeps the comment being written on ticket #n; an empty text forgets it. */
  setComment(n: number, text: string): void {
    if (text === '') comments.delete(n)
    else comments.set(n, text)
  },
  /** Forgets every draft (for tests). */
  clear(): void {
    descriptions.clear()
    comments.clear()
  },
}
