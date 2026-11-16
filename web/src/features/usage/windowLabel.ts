// What a usage window is called. A script names each window technically
// (`fable_weekly`: what a model's usage_windows and the per-window limits
// refer to) and may give it a label for people ("Weekly, Fable"). Both come
// from the script, so every text here goes through visibleText.
import type { UsageReading, UsageWindow } from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'

type Named = Pick<UsageWindow, 'name'> & { label?: string }

/** The window as people read it: its label, else its name. */
export function windowTitle(w: Named): string {
  return visibleText(w.label ? w.label : w.name)
}

/** "label (name)" where the window has a label, else its name: where the technical name matters too. */
export function windowWithName(w: Named): string {
  return w.label ? `${visibleText(w.label)} (${visibleText(w.name)})` : visibleText(w.name)
}

/** The label of each window name in the current readings; the first account that labels a name wins. */
export function labelsOf(readings: readonly Pick<UsageReading, 'windows'>[] | null | undefined): Map<string, string> {
  const out = new Map<string, string>()
  for (const r of readings ?? []) for (const w of r.windows) if (w.label && !out.has(w.name)) out.set(w.name, w.label)
  return out
}

/** A window known only by its name, as "label (name)" when the current readings label it. */
export function nameWithLabel(name: string, labels: ReadonlyMap<string, string>): string {
  return windowWithName({ name, label: labels.get(name) ?? '' })
}

/** The windows a profile's account reports now, in words for the model editor; empty when there is no reading. */
export function reportedWords(profile: string, readings: readonly Pick<UsageReading, 'profiles' | 'windows'>[] | null | undefined): string {
  const r = (readings ?? []).find((x) => x.profiles.includes(profile))
  if (!r || r.windows.length === 0) return ''
  return `Its script reports now: ${r.windows.map(windowWithName).join('; ')}.`
}
