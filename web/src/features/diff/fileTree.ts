// The changed files of a ticket as a folder tree, for the file browser on the
// ticket's page (like the file tree of a pull request): per project when the
// ticket has several, then Committed and Uncommitted, then folders and files.
// A folder whose only content is one folder is shown as one entry
// ("src/features/diff"). Folders come before files, each sorted by name.
//
// Paths are the server's (already escaped where they hold bytes that are not
// text); they are split at "/" here and every name still goes through
// visibleText where it is shown.
import type { DiffFile, ProjectDiff } from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'

export interface TreeFile {
  kind: 'file'
  /** Unique in the tree; the file's key in the diff (see `fileKey`). */
  id: string
  /** The last part of the path. */
  name: string
  file: DiffFile
  project: string
  uncommitted: boolean
}

export interface TreeFolder {
  kind: 'folder'
  id: string
  /** One folder, or a chain of folders that hold nothing else ("a/b/c"). */
  name: string
  children: TreeNode[]
  /** Files anywhere below. */
  files: number
}

/** A project of the ticket, or the committed or uncommitted part of one. */
export interface TreeGroup {
  kind: 'group'
  id: string
  name: string
  children: TreeNode[]
  files: number
  /** Said under the name: the project's state when it has no diff, a truncation. */
  note?: string
  /** What the group is: a project, or a section of one. */
  role: 'project' | 'committed' | 'uncommitted'
  project: string
}

export type TreeNode = TreeFile | TreeFolder | TreeGroup
export type TreeParent = TreeFolder | TreeGroup

export interface FileInput {
  /** The file's key in the diff. */
  key: string
  file: DiffFile
  project: string
  uncommitted: boolean
}

const byName = (a: string, b: string) => a.localeCompare(b, 'en', { sensitivity: 'base', numeric: true }) || (a < b ? -1 : a > b ? 1 : 0)

function sortNodes(list: TreeNode[]): TreeNode[] {
  return list.sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === 'folder' ? -1 : b.kind === 'folder' ? 1 : 0
    return byName(a.name, b.name)
  })
}

interface Building {
  folders: Map<string, Building>
  files: TreeFile[]
}

function toNodes(b: Building, idOf: (path: string) => string, at: string): TreeNode[] {
  const out: TreeNode[] = [...b.files]
  for (const [name, sub] of b.folders) {
    let path = at === '' ? name : `${at}/${name}`
    let label = name
    let node = sub
    // A folder holding only one folder and no file shows as one entry.
    while (node.files.length === 0 && node.folders.size === 1) {
      const [nextName, next] = [...node.folders][0]!
      path = `${path}/${nextName}`
      label = `${label}/${nextName}`
      node = next
    }
    const children = toNodes(node, idOf, path)
    out.push({ kind: 'folder', id: idOf(path), name: label, children, files: countFiles(children) })
  }
  return sortNodes(out)
}

function countFiles(list: TreeNode[]): number {
  return list.reduce((n, c) => n + (c.kind === 'file' ? 1 : c.files), 0)
}

/** The files as folders and files; `idPrefix` keeps folder ids apart between trees. */
export function buildFileTree(files: FileInput[], idPrefix: string): TreeNode[] {
  const root: Building = { folders: new Map(), files: [] }
  for (const f of files) {
    const parts = f.file.path.split('/').filter((p) => p !== '')
    const name = parts.pop() ?? f.file.path
    let at = root
    for (const p of parts) {
      let next = at.folders.get(p)
      if (!next) {
        next = { folders: new Map(), files: [] }
        at.folders.set(p, next)
      }
      at = next
    }
    at.files.push({ kind: 'file', id: f.key, name, file: f.file, project: f.project, uncommitted: f.uncommitted })
  }
  return toNodes(root, (path) => `${idPrefix}\u0000${path}`, '')
}

/** Whether a file's path (as shown) contains the filter, ignoring case; an empty filter keeps all. */
export function matchesFilter(f: DiffFile, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (q === '') return true
  return visibleText(f.path).toLowerCase().includes(q) || (f.old_path !== '' && visibleText(f.old_path).toLowerCase().includes(q))
}

export interface TreeOptions {
  /** The file's key in the diff. */
  keyOf(project: string, uncommitted: boolean, path: string): string
  /** A project's name as shown. */
  nameOf(project: string): string
  /** The filter typed; files whose path does not contain it are left out. */
  filter?: string
}

/** Why a project has no committed files to list, in words; empty when it has a diff. */
function stateNote(p: ProjectDiff): string {
  return p.state === 'ok' ? '' : visibleText(p.message) || 'No diff.'
}

/**
 * The whole tree of a ticket's diff: with several projects one group per
 * project, each with its Committed and (when there is any) Uncommitted part;
 * with one project those parts alone. A project or part with no file left
 * after the filter is left out while filtering.
 */
export function diffTree(projects: ProjectDiff[], o: TreeOptions): TreeNode[] {
  const filter = o.filter ?? ''
  const filtering = filter.trim() !== ''
  const groupsOf = (p: ProjectDiff): TreeGroup[] => {
    const out: TreeGroup[] = []
    const part = (uncommitted: boolean, list: DiffFile[], truncated: boolean) => {
      const kept = list.filter((f) => matchesFilter(f, filter))
      if (filtering && kept.length === 0) return
      const id = `${p.project}\u0000${uncommitted ? 'u' : 'c'}`
      const children = buildFileTree(
        kept.map((f) => ({ key: o.keyOf(p.project, uncommitted, f.path), file: f, project: p.project, uncommitted })),
        id,
      )
      const note = truncated ? 'More files changed than can be listed.' : undefined
      out.push({
        kind: 'group',
        id,
        name: uncommitted ? 'Uncommitted' : 'Committed',
        children,
        files: countFiles(children),
        role: uncommitted ? 'uncommitted' : 'committed',
        project: p.project,
        ...(note ? { note } : {}),
      })
    }
    if (p.state === 'ok') part(false, p.files, p.files_truncated)
    if (p.uncommitted.length > 0 || p.uncommitted_truncated) part(true, p.uncommitted, p.uncommitted_truncated)
    return out
  }
  if (projects.length === 1) return groupsOf(projects[0]!)
  const out: TreeNode[] = []
  for (const p of projects) {
    const children = groupsOf(p)
    if (filtering && children.length === 0) continue
    const note = stateNote(p)
    out.push({
      kind: 'group',
      id: `${p.project}\u0000project`,
      name: o.nameOf(p.project),
      children,
      files: countFiles(children),
      role: 'project',
      project: p.project,
      ...(note ? { note } : {}),
    })
  }
  return out
}

/** Every file of a tree, in the order the tree shows them. */
export function treeFiles(list: TreeNode[]): TreeFile[] {
  const out: TreeFile[] = []
  for (const n of list) {
    if (n.kind === 'file') out.push(n)
    else out.push(...treeFiles(n.children))
  }
  return out
}

/** One line of the tree as shown: a node with its place (for the tree's ARIA attributes and keys). */
export interface TreeRow {
  node: TreeNode
  /** 1 for the top. */
  level: number
  /** The row index of the node's parent; -1 at the top. */
  parent: number
  /** Position among its siblings, from 1, and their number. */
  posInSet: number
  setSize: number
}

/** The rows shown: every node whose parents are all expanded, in order. */
export function visibleRows(list: TreeNode[], expanded: (id: string) => boolean): TreeRow[] {
  const out: TreeRow[] = []
  const walk = (nodes: TreeNode[], level: number, parent: number) => {
    nodes.forEach((node, i) => {
      const at = out.length
      out.push({ node, level, parent, posInSet: i + 1, setSize: nodes.length })
      if (node.kind !== 'file' && expanded(node.id)) walk(node.children, level + 1, at)
    })
  }
  walk(list, 1, -1)
  return out
}

/** What a key on the focused row does (the tree pattern of ARIA): move the focus, open or close a folder, or open a file. */
export type TreeAction =
  | { kind: 'focus'; index: number }
  | { kind: 'expand'; id: string }
  | { kind: 'collapse'; id: string }
  | { kind: 'open'; id: string }
  | { kind: 'none' }

export function treeKey(rows: TreeRow[], index: number, key: string, expanded: (id: string) => boolean): TreeAction {
  const row = rows[index]
  if (!row) return { kind: 'none' }
  const node = row.node
  const branch = node.kind !== 'file'
  switch (key) {
    case 'ArrowDown':
      return index + 1 < rows.length ? { kind: 'focus', index: index + 1 } : { kind: 'none' }
    case 'ArrowUp':
      return index > 0 ? { kind: 'focus', index: index - 1 } : { kind: 'none' }
    case 'Home':
      return { kind: 'focus', index: 0 }
    case 'End':
      return { kind: 'focus', index: rows.length - 1 }
    case 'ArrowRight':
      if (!branch) return { kind: 'none' }
      if (!expanded(node.id)) return { kind: 'expand', id: node.id }
      return node.children.length > 0 ? { kind: 'focus', index: index + 1 } : { kind: 'none' }
    case 'ArrowLeft':
      if (branch && expanded(node.id)) return { kind: 'collapse', id: node.id }
      return row.parent >= 0 ? { kind: 'focus', index: row.parent } : { kind: 'none' }
    case 'Enter':
    case ' ':
      if (!branch) return { kind: 'open', id: node.id }
      return expanded(node.id) ? { kind: 'collapse', id: node.id } : { kind: 'expand', id: node.id }
    default:
      return { kind: 'none' }
  }
}
