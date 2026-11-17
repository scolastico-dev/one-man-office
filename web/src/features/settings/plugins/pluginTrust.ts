// What the Plugins page says about where a plugin comes from: the tags of
// omo's plugin list in words, the warning before a plugin that is not
// official is installed, and the question that shows it. The server decides
// a source's tag itself and refuses an install without the acknowledgement;
// the page only asks first. Every text from the list (names, descriptions,
// addresses) is data from the network and is shown as plain text.
import { inject, type InjectionKey } from 'vue'
import { ApiError } from '../../../api/client'
import type { CatalogPlugin, PluginSettings, PluginTag } from '../../../api/types'
import type { DialogScope } from '../../../components/ui/mountDialog'
import type { StateWords } from '../extensionWords'

/** A tag as a badge. */
export const TAG_WORDS: Readonly<Record<PluginTag, StateWords & { meaning: string }>> = {
  official: { tone: 'ok', text: 'official', meaning: "Kept in omo's repository by omo's maintainers." },
  recommended: { tone: 'accent', text: 'recommended', meaning: "Someone else's plugin that omo's maintainers looked at." },
  community: { tone: 'warn', text: 'community', meaning: "Listed, not reviewed by omo's maintainers." },
}

/**
 * The tag omo gives a plugin of the list, the only place the page decides
 * it: the tag of omo's own list, and community for a plugin of an additional
 * list (`list` set), whatever that list says. The badge and the question
 * before an install follow from it; the server decides the same
 * (`pluginmanager.TrustTag`) and refuses an install without the
 * acknowledgement.
 */
export function trustTagOf(p: Pick<CatalogPlugin, 'tag' | 'list'>): PluginTag {
  if (p.list !== '') return 'community'
  return p.tag === 'official' || p.tag === 'recommended' ? p.tag : 'community'
}

/** What adding an additional plugin list means, in one paragraph (the community warning's words). */
export function listWarning(): string {
  return `omo fetches each address and shows the plugins of that list as community plugins, whatever the list says: nobody reviewed them, and omo asks before it installs one. ${trustWarning('community').paragraphs[0]} Review a plugin before installing it.`
}

/** The acknowledgement an install of a plugin with `tag` sends: none for an official one. */
export function acknowledgementOf(tag: PluginTag): Exclude<PluginTag, 'official'> | undefined {
  return tag === 'official' ? undefined : tag
}

/** The tag of a refusal that asks for the user's acknowledgement, else null. */
export function confirmationOf(e: unknown): PluginTag | null {
  if (!(e instanceof ApiError) || e.code !== 'validation') return null
  const d = e.details as { field?: unknown; tag?: unknown } | undefined
  if (d?.field !== 'acknowledge') return null
  return d.tag === 'recommended' || d.tag === 'official' ? d.tag : 'community'
}

/** Whether a plugin of the list is installed: a plugin uses its source, folder and ref. */
export function installedFrom(p: Pick<CatalogPlugin, 'source' | 'subpath' | 'ref'>, settings: readonly Pick<PluginSettings, 'source' | 'subpath' | 'ref'>[]): boolean {
  return settings.some((s) => s.source === p.source && s.subpath === p.subpath && s.ref === p.ref)
}

/** Where a plugin comes from, in words: the address, then the branch and the folder when there are. */
export function whereFrom(p: Pick<CatalogPlugin, 'source' | 'subpath' | 'ref'>): string {
  const parts = [p.source]
  parts.push(p.ref === '' ? 'default branch' : `branch or tag ${p.ref}`)
  if (p.subpath !== '') parts.push(`folder ${p.subpath}`)
  return parts.join(', ')
}

/** The warning before a plugin that is not official is installed, in paragraphs. */
export function trustWarning(tag: Exclude<PluginTag, 'official'>): { title: string; paragraphs: string[] } {
  const runs = 'A plugin’s hooks run commands on this computer as you, with your rights: they can read and change your files and use your accounts.'
  if (tag === 'recommended')
    return {
      title: 'This plugin is someone else’s code',
      paragraphs: [runs, 'omo’s maintainers looked at this plugin, but it is written and kept by someone else, who can change it with every update.'],
    }
  return {
    title: 'Nobody reviewed this plugin',
    paragraphs: [
      runs,
      'omo’s maintainers have not reviewed this plugin. Install it only when you know and trust where it comes from, and read its code first if you can.',
    ],
  }
}

/** What the question is about. */
export interface TrustQuestion {
  name: string
  tag: Exclude<PluginTag, 'official'>
  source: string
  ref: string
  subpath: string
  /** Install (a new plugin) or Update (fetching an installed one from a changed source). */
  verb: 'Install' | 'Update'
}

/** The dialogs of the Plugins page; the page provides its scope, its parts ask through it. */
export const PLUGIN_DIALOGS: InjectionKey<DialogScope> = Symbol('plugin dialogs')

/** The page's dialog scope; null outside the Plugins page (then nothing is asked and nothing is installed). */
export function usePluginDialogs(): DialogScope | null {
  return inject(PLUGIN_DIALOGS, null)
}
