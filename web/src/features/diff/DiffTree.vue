<script setup lang="ts">
// The changed files as a folder tree (the ARIA tree pattern): one row per
// project, part (committed, uncommitted), folder and file; the arrow keys
// move along the rows, Right and Left open and close folders, Enter or
// Space (or a click) opens a file. The file shown beside the tree is marked
// as selected. Every name is shown through visibleText.
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { visibleText } from '../../components/ui/visibleText'
import { fileStatus } from './fileStatus'
import { treeKey, visibleRows, type TreeNode, type TreeRow } from './fileTree'

const props = defineProps<{
  nodes: TreeNode[]
  /** The id of the file shown. */
  selected: string | null
  /** Accessible name of the tree. */
  label: string
  /** A filter is typed: every folder shows open. */
  filtering?: boolean
}>()
const emit = defineEmits<{ open: [id: string] }>()

/** Folders and groups the user closed; everything else is open. */
const closed = reactive(new Set<string>())
const expanded = (id: string) => props.filtering === true || !closed.has(id)
const rows = computed<TreeRow[]>(() => visibleRows(props.nodes, expanded))

/** The one row in the tab order. */
const focusIndex = ref(0)
const list = ref<HTMLElement>()

function rowEl(i: number): HTMLElement | undefined {
  return list.value?.querySelectorAll<HTMLElement>('[role="treeitem"]')[i]
}

function focusRow(i: number) {
  focusIndex.value = i
  void nextTick(() => rowEl(i)?.focus())
}

// The file shown changed (from here, the previous/next buttons, a reload): its row takes the tab stop and comes into view.
watch(
  () => [props.selected, rows.value] as const,
  ([id]) => {
    const i = rows.value.findIndex((r) => r.node.id === id)
    if (i >= 0) {
      focusIndex.value = i
      void nextTick(() => rowEl(i)?.scrollIntoView({ block: 'nearest' }))
    } else if (focusIndex.value >= rows.value.length) focusIndex.value = 0
  },
  { immediate: true },
)

function setOpen(id: string, open: boolean) {
  if (open) closed.delete(id)
  else closed.add(id)
}

function activate(row: TreeRow, i: number) {
  focusIndex.value = i
  if (row.node.kind === 'file') emit('open', row.node.id)
  else setOpen(row.node.id, !expanded(row.node.id))
}

function onKeydown(e: KeyboardEvent, i: number) {
  if (e.ctrlKey || e.metaKey || e.altKey) return
  const action = treeKey(rows.value, i, e.key, expanded)
  if (action.kind === 'none') return
  e.preventDefault()
  if (action.kind === 'focus') focusRow(action.index)
  else if (action.kind === 'expand') setOpen(action.id, true)
  else if (action.kind === 'collapse') setOpen(action.id, false)
  else emit('open', action.id)
}

const fmt = (n: number) => n.toLocaleString('en')
const files = (n: number) => (n === 1 ? '1 file' : `${fmt(n)} files`)
const indent = (level: number) => ({ paddingLeft: `calc(var(--omo-space) * ${2 + (level - 1) * 3})` })

function titleOf(node: TreeNode): string | undefined {
  if (node.kind !== 'file') return undefined
  const path = visibleText(node.file.path)
  return node.file.old_path ? `${path}, renamed from ${visibleText(node.file.old_path)}` : path
}
</script>

<template>
  <ul ref="list" class="dtree" role="tree" :aria-label="label" data-diff-tree>
    <li
      v-for="(row, i) in rows"
      :key="row.node.id"
      role="treeitem"
      class="dtree__row"
      :class="[`dtree__row--${row.node.kind}`, { 'dtree__row--current': row.node.id === selected }]"
      :aria-level="row.level"
      :aria-setsize="row.setSize"
      :aria-posinset="row.posInSet"
      :aria-expanded="row.node.kind === 'file' ? undefined : expanded(row.node.id) ? 'true' : 'false'"
      :aria-selected="row.node.kind === 'file' ? (row.node.id === selected ? 'true' : 'false') : undefined"
      :tabindex="i === focusIndex ? 0 : -1"
      :title="titleOf(row.node)"
      :style="indent(row.level)"
      :data-tree-file="row.node.kind === 'file' ? row.node.file.path : undefined"
      :data-tree-folder="row.node.kind === 'folder' ? row.node.name : undefined"
      :data-tree-group="row.node.kind === 'group' ? row.node.role : undefined"
      :data-current="row.node.id === selected ? '' : undefined"
      @click="activate(row, i)"
      @keydown="onKeydown($event, i)"
    >
      <template v-if="row.node.kind === 'file'">
        <span class="dtree__status" :class="`dtree__status--${row.node.file.status}`" data-status
          ><span aria-hidden="true">{{ fileStatus(row.node.file.status)[0] }}</span
          ><span class="sr-only">{{ fileStatus(row.node.file.status)[1] }}</span></span
        >
        <span class="dtree__name" data-tree-name>{{ visibleText(row.node.name) }}</span>
        <span v-if="row.node.file.binary" class="dtree__note">bin</span>
        <span v-else-if="row.node.file.status === 'untracked'" class="dtree__note">new</span>
        <span v-else class="dtree__counts"
          ><span class="dtree__add">+{{ fmt(row.node.file.additions) }}</span> <span class="dtree__del">−{{ fmt(row.node.file.deletions) }}</span></span
        >
      </template>
      <template v-else>
        <span class="dtree__caret" aria-hidden="true">{{ expanded(row.node.id) ? '▾' : '▸' }}</span>
        <span class="dtree__name" :class="{ 'dtree__name--group': row.node.kind === 'group' }">{{
          row.node.kind === 'group' ? row.node.name : visibleText(row.node.name)
        }}</span>
        <span class="dtree__note">{{ row.node.kind === 'group' && row.node.note && row.node.files === 0 ? visibleText(row.node.note) : files(row.node.files) }}</span>
      </template>
    </li>
  </ul>
</template>

<style scoped>
.dtree {
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.dtree__row {
  display: flex;
  align-items: baseline;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
  padding-top: calc(var(--omo-space) * 0.75);
  padding-right: calc(var(--omo-space) * 2);
  padding-bottom: calc(var(--omo-space) * 0.75);
  border-left: var(--omo-edge) solid transparent;
  cursor: pointer;
  user-select: none;
}
.dtree__row:hover {
  background: var(--omo-raised);
}
.dtree__row:focus-visible {
  outline: var(--omo-focus-width) solid var(--omo-accent);
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.dtree__row--current {
  border-left-color: var(--omo-accent);
  background: var(--omo-accent-soft);
}
.dtree__caret {
  flex-shrink: 0;
  width: 1ch;
  color: var(--omo-muted);
}
.dtree__status {
  flex-shrink: 0;
  width: 1ch;
  color: var(--omo-muted);
  font-weight: 700;
}
.dtree__status--added,
.dtree__status--untracked {
  color: var(--omo-ok);
}
.dtree__status--deleted {
  color: var(--omo-danger);
}
.dtree__status--modified,
.dtree__status--renamed {
  color: var(--omo-accent);
}
.dtree__name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dtree__row--folder .dtree__name {
  color: var(--omo-muted);
}
.dtree__name--group {
  font-weight: 700;
}
.dtree__counts,
.dtree__note {
  flex-shrink: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.dtree__note {
  color: var(--omo-muted);
}
.dtree__add {
  color: var(--omo-ok);
}
.dtree__del {
  color: var(--omo-danger);
}
</style>
