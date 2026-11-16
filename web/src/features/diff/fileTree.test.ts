import { expect, test } from 'vitest'
import type { DiffFile, ProjectDiff } from '../../api/types'
import { buildFileTree, diffTree, matchesFilter, treeFiles, treeKey, visibleRows, type TreeNode } from './fileTree'
import { fileKey } from './useDiff'

const file = (path: string, more: Partial<DiffFile> = {}): DiffFile => ({
  path,
  old_path: '',
  status: 'modified',
  binary: false,
  additions: 1,
  deletions: 0,
  patch: '',
  truncated: false,
  ...more,
})

const input = (paths: string[]) => paths.map((p) => ({ key: `k:${p}`, file: file(p), project: 'p', uncommitted: false }))

/** The tree as indented lines: folders end in "/". */
function outline(nodes: TreeNode[], depth = 0): string[] {
  return nodes.flatMap((n) => [`${'  '.repeat(depth)}${n.name}${n.kind === 'file' ? '' : '/'}`, ...(n.kind === 'file' ? [] : outline(n.children, depth + 1))])
}

const project = (id: string, more: Partial<ProjectDiff> = {}): ProjectDiff => ({
  project: id,
  base: 'main',
  branch: `omo/${id}`,
  base_commit: 'a',
  head_commit: 'b',
  state: 'ok',
  message: '',
  files: [],
  files_truncated: false,
  uncommitted: [],
  uncommitted_truncated: false,
  compared_with: 'local',
  ...more,
})

test('nested folders become a tree, folders before files, each sorted by name', () => {
  const tree = buildFileTree(input(['web/src/b.ts', 'README.md', 'web/src/a.ts', 'web/package.json', 'go.mod', 'web/e2e/x.spec.ts']), 'p')
  expect(outline(tree)).toEqual(['web/', '  e2e/', '    x.spec.ts', '  src/', '    a.ts', '    b.ts', '  package.json', 'go.mod', 'README.md'])
  expect(tree[0]!.kind === 'folder' && tree[0]!.files).toBe(4)
})

test('a chain of folders holding only one folder is one entry, as on a pull request page', () => {
  const tree = buildFileTree(input(['web/src/features/diff/fileTree.ts', 'web/src/features/diff/DiffView.vue', 'docs/a/b/c.md']), 'p')
  expect(outline(tree)).toEqual(['docs/a/b/', '  c.md', 'web/src/features/diff/', '  DiffView.vue', '  fileTree.ts'])
  // The chain stops at a folder that holds a file besides its one folder.
  expect(outline(buildFileTree(input(['a/b/c/d.ts', 'a/b/e.ts']), 'p'))).toEqual(['a/b/', '  c/', '    d.ts', '  e.ts'])
})

test('a renamed file sits at its new path and keeps its old one', () => {
  const tree = buildFileTree([{ key: 'k', file: file('new/place.ts', { status: 'renamed', old_path: 'old/place.ts' }), project: 'p', uncommitted: false }], 'p')
  expect(outline(tree)).toEqual(['new/', '  place.ts'])
  const f = treeFiles(tree)[0]!
  expect(f.file.old_path).toBe('old/place.ts')
  expect(f.id).toBe('k')
})

test('many files in many folders are all in the tree, counted per folder', () => {
  const paths: string[] = []
  for (let d = 0; d < 30; d++) for (let f = 0; f < 40; f++) paths.push(`src/dir${d}/file${f}.ts`)
  const tree = buildFileTree(input(paths), 'p')
  expect(treeFiles(tree)).toHaveLength(1200)
  expect(tree).toHaveLength(1)
  const src = tree[0]!
  expect(src.kind === 'folder' && src.files).toBe(1200)
  // Numbers sort as numbers.
  expect(src.kind === 'folder' && src.children.slice(0, 3).map((c) => c.name)).toEqual(['dir0', 'dir1', 'dir2'])
})

test('names with hidden characters stay as they are in the tree (shown through visibleText) and the filter reads them as shown', () => {
  const bidi = 'x y\u202e.txt'
  const tree = buildFileTree(input([`a\u200b/${bidi}`, 'a/plain.txt']), 'p')
  // Two different folders: one with a zero-width space in its name.
  expect(tree.map((n) => n.name)).toEqual(['a', 'a\u200b'])
  expect(matchesFilter(file(`a/${bidi}`), '\\u{202e}')).toBe(true)
  expect(matchesFilter(file(`a/${bidi}`), 'zzz')).toBe(false)
})

test('empty path parts do not make empty folders', () => {
  expect(outline(buildFileTree(input(['a//b.ts', '/c.ts']), 'p'))).toEqual(['a/', '  b.ts', 'c.ts'])
})

const opts = { keyOf: fileKey, nameOf: (id: string) => id.toUpperCase() }

test('one project: its Committed and Uncommitted parts at the top', () => {
  const tree = diffTree([project('api', { files: [file('a.go')], uncommitted: [file('b.go', { status: 'untracked' })] })], opts)
  expect(outline(tree)).toEqual(['Committed/', '  a.go', 'Uncommitted/', '  b.go'])
  expect(treeFiles(tree).map((f) => [f.id, f.uncommitted])).toEqual([
    [fileKey('api', false, 'a.go'), false],
    [fileKey('api', true, 'b.go'), true],
  ])
})

test('several projects: one group each, with the state of a project that has no diff', () => {
  const tree = diffTree(
    [
      project('api', { files: [file('cmd/main.go')] }),
      project('web', { state: 'not_started', message: 'No branch yet.' }),
      project('ui', { files: [file('x.ts')], files_truncated: true }),
    ],
    opts,
  )
  expect(outline(tree)).toEqual(['API/', '  Committed/', '    cmd/', '      main.go', 'WEB/', 'UI/', '  Committed/', '    x.ts'])
  expect(tree[1]).toMatchObject({ kind: 'group', role: 'project', note: 'No branch yet.', files: 0 })
  const ui = tree[2]!
  expect(ui.kind === 'group' && ui.children[0]).toMatchObject({ note: 'More files changed than can be listed.' })
})

test('the filter keeps matching files and the folders and groups above them, and leaves out the rest', () => {
  const projects = [project('api', { files: [file('cmd/main.go'), file('cmd/util.go')] }), project('web', { files: [file('src/main.ts')], uncommitted: [file('README.md')] })]
  expect(outline(diffTree(projects, { ...opts, filter: 'MAIN' }))).toEqual(['API/', '  Committed/', '    cmd/', '      main.go', 'WEB/', '  Committed/', '    src/', '      main.ts'])
  expect(diffTree(projects, { ...opts, filter: 'nothing' })).toEqual([])
  // A renamed file is found by its old name too.
  expect(matchesFilter(file('b.ts', { old_path: 'old-name.ts' }), 'old-name')).toBe(true)
})

test('rows follow the folders that are open, with level, parent and place among siblings', () => {
  const tree = buildFileTree(input(['a/x.ts', 'a/y.ts', 'b.ts']), 'p')
  const open = new Set(tree.filter((n) => n.kind !== 'file').map((n) => n.id))
  const rows = visibleRows(tree, (id) => open.has(id))
  expect(rows.map((r) => [r.node.name, r.level, r.parent, r.posInSet, r.setSize])).toEqual([
    ['a', 1, -1, 1, 2],
    ['x.ts', 2, 0, 1, 2],
    ['y.ts', 2, 0, 2, 2],
    ['b.ts', 1, -1, 2, 2],
  ])
  expect(visibleRows(tree, () => false).map((r) => r.node.name)).toEqual(['a', 'b.ts'])
})

test('the keys of the tree: arrows move, right opens or goes in, left closes or goes up, Enter opens a file', () => {
  const tree = buildFileTree(input(['a/x.ts', 'a/y.ts', 'b.ts']), 'p')
  const folder = tree[0]!.id
  const open = new Set([folder])
  const expanded = (id: string) => open.has(id)
  const rows = visibleRows(tree, expanded)
  expect(treeKey(rows, 0, 'ArrowDown', expanded)).toEqual({ kind: 'focus', index: 1 })
  expect(treeKey(rows, 3, 'ArrowDown', expanded)).toEqual({ kind: 'none' })
  expect(treeKey(rows, 1, 'ArrowUp', expanded)).toEqual({ kind: 'focus', index: 0 })
  expect(treeKey(rows, 0, 'ArrowUp', expanded)).toEqual({ kind: 'none' })
  expect(treeKey(rows, 2, 'Home', expanded)).toEqual({ kind: 'focus', index: 0 })
  expect(treeKey(rows, 0, 'End', expanded)).toEqual({ kind: 'focus', index: 3 })
  // On an open folder: right goes to its first child, left closes it.
  expect(treeKey(rows, 0, 'ArrowRight', expanded)).toEqual({ kind: 'focus', index: 1 })
  expect(treeKey(rows, 0, 'ArrowLeft', expanded)).toEqual({ kind: 'collapse', id: folder })
  // On a file: left goes to its folder, right does nothing, Enter and Space open it.
  expect(treeKey(rows, 2, 'ArrowLeft', expanded)).toEqual({ kind: 'focus', index: 0 })
  expect(treeKey(rows, 2, 'ArrowRight', expanded)).toEqual({ kind: 'none' })
  expect(treeKey(rows, 2, 'Enter', expanded)).toEqual({ kind: 'open', id: 'k:a/y.ts' })
  expect(treeKey(rows, 3, ' ', expanded)).toEqual({ kind: 'open', id: 'k:b.ts' })
  // On a closed folder: right opens it; Enter toggles; left on the top level does nothing.
  open.clear()
  const closed = visibleRows(tree, expanded)
  expect(treeKey(closed, 0, 'ArrowRight', expanded)).toEqual({ kind: 'expand', id: folder })
  expect(treeKey(closed, 0, 'Enter', expanded)).toEqual({ kind: 'expand', id: folder })
  expect(treeKey(closed, 0, 'ArrowLeft', expanded)).toEqual({ kind: 'none' })
  expect(treeKey(closed, 0, 'x', expanded)).toEqual({ kind: 'none' })
})
