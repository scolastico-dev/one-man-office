<script setup lang="ts">
// The Diff tab's content: per project of the ticket, what its branch changed
// since it left its base (committed), and what is changed in its worktree
// but not committed. Several projects get a switcher. Branch names, file
// names, lines and git's messages come from the agent's work and are shown as
// text with control and reordering characters escaped.
import { computed, nextTick, useId } from 'vue'
import type { DiffFile, ProjectDiff, TicketSummary } from '../../api/types'
import UiTabs, { type UiTab } from '../../components/ui/UiTabs.vue'
import { revealInBox } from '../../components/ui/scrollBox'
import { visibleText } from '../../components/ui/visibleText'
import { useBoard } from '../../store'
import DiffFileList, { type FileEntry } from './DiffFileList.vue'
import DiffFrame from './DiffFrame.vue'
import DiffFileView from './DiffFileView.vue'
import DiffProjectHead from './DiffProjectHead.vue'
import { changedCount, fileKey, startsOpen, useDiff } from './useDiff'

const props = defineProps<{ ticket: TicketSummary }>()

const store = useBoard()
const d = useDiff(props.ticket.number)
const uid = useId()

/** More files than this in a section and the section gets a list of jumps. */
const LIST_FROM = 4

const projects = computed(() => d.diff.value?.projects ?? [])
const nameOf = (id: string) => visibleText(store.state.projects.get(id)?.name ?? id)
const tabs = computed<UiTab[]>(() =>
  projects.value.map((p) => {
    const count = changedCount(p)
    return count === undefined ? { id: p.project, label: nameOf(p.project) } : { id: p.project, label: nameOf(p.project), count }
  }),
)

interface Section {
  id: 'committed' | 'uncommitted'
  title: string
  entries: FileEntry[]
  truncated: boolean
  additions: number
  deletions: number
}

function entries(p: ProjectDiff, uncommitted: boolean, files: DiffFile[]): FileEntry[] {
  return files.map((f, i) => {
    const key = fileKey(p.project, uncommitted, f.path)
    return { key, domId: `${uid}-${uncommitted ? 'u' : 'c'}${i}`, file: d.fileOf(key, f) }
  })
}

function sum(list: DiffFile[], field: 'additions' | 'deletions'): number {
  return list.reduce((n, f) => n + f[field], 0)
}

const sections = computed<Section[]>(() => {
  const p = d.current.value
  if (!p) return []
  const out: Section[] = []
  if (p.state === 'ok') {
    out.push({
      id: 'committed',
      title: 'Committed',
      entries: entries(p, false, p.files),
      truncated: p.files_truncated,
      additions: sum(p.files, 'additions'),
      deletions: sum(p.files, 'deletions'),
    })
  }
  if (p.uncommitted.length > 0 || p.uncommitted_truncated) {
    out.push({
      id: 'uncommitted',
      title: 'Uncommitted in the worktree',
      entries: entries(p, true, p.uncommitted),
      truncated: p.uncommitted_truncated,
      additions: sum(p.uncommitted, 'additions'),
      deletions: sum(p.uncommitted, 'deletions'),
    })
  }
  return out
})

/** Files in the selected project: many of them and every file starts closed. */
const fileCount = computed(() => {
  const p = d.current.value
  return p ? p.files.length + p.uncommitted.length : 0
})

const files = (n: number) => (n === 1 ? '1 file' : `${n.toLocaleString('en')} files`)

function isOpen(e: FileEntry): boolean {
  return d.isOpen(e.key, startsOpen(e.file, fileCount.value))
}

function load(e: FileEntry, uncommitted: boolean) {
  void d.loadFile(d.current.value!.project, e.file.path, uncommitted)
}

async function jump(key: string, list: FileEntry[]) {
  const e = list.find((x) => x.key === key)
  if (!e) return
  d.setOpen(key, true)
  await nextTick()
  const el = document.getElementById(e.domId)
  if (el) revealInBox(el, 'start')
  el?.querySelector<HTMLElement>('[data-file-toggle]')?.focus({ preventScroll: true })
}

function selectProject(id: string) {
  d.project.value = id
}
</script>

<template>
  <div class="sdiff" data-diff>
    <DiffFrame :d="d">
      <p v-if="projects.length === 0" class="sdiff__note">This ticket has no project, so there is nothing to compare.</p>
      <UiTabs
        v-if="projects.length > 1"
        :model-value="d.current.value?.project ?? ''"
        :tabs="tabs"
        :label="`Projects of #${ticket.number}`"
        @update:model-value="selectProject"
      />
      <section v-if="d.current.value" class="sdiff__project" :aria-label="nameOf(d.current.value.project)" :data-diff-project="d.current.value.project">
        <DiffProjectHead :d="d" :project="d.current.value" :ticket="ticket" :name="nameOf(d.current.value.project)" />

        <section v-for="s in sections" :key="s.id" class="sdiff__section" :data-diff-section="s.id">
          <h5 class="sdiff__group">{{ s.title }}</h5>
          <p class="sdiff__sum" data-diff-totals>
            {{ files(s.entries.length) }},
            <span class="sdiff__add">+{{ s.additions.toLocaleString('en') }}</span> <span class="sdiff__del">−{{ s.deletions.toLocaleString('en') }}</span>
          </p>
          <p v-if="s.truncated" class="sdiff__note" data-diff-truncated>
            More files changed than can be listed here; only the first {{ files(s.entries.length) }} are shown.
          </p>
          <p v-if="s.entries.length === 0 && s.id === 'committed'" class="sdiff__note">
            {{ d.current.value?.recorded ? 'No changes were recorded.' : 'No commits of its own yet.' }}
          </p>
          <DiffFileList v-if="s.entries.length >= LIST_FROM" :entries="s.entries" :label="`${s.title}: files`" @jump="jump($event, s.entries)" />
          <ul v-if="s.entries.length > 0" class="sdiff__list">
            <DiffFileView
              v-for="e in s.entries"
              :key="e.key"
              :file="e.file"
              :dom-id="e.domId"
              :open="isOpen(e)"
              :loading="d.loadState(e.key).loading"
              :loaded="d.loadState(e.key).loaded"
              :error="d.loadState(e.key).error"
              :uncommitted="s.id === 'uncommitted'"
              :expansion="d.expansionOf(e.key, e.file)"
              @toggle="d.setOpen(e.key, $event)"
              @more="load(e, s.id === 'uncommitted')"
              @expand="void d.expand(d.current.value!.project, e.file, s.id === 'uncommitted', $event)"
            />
          </ul>
        </section>
      </section>
    </DiffFrame>
  </div>
</template>

<style scoped>
.sdiff {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.sdiff__project,
.sdiff__section {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.sdiff__group {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.sdiff__group {
  margin-top: calc(var(--omo-space) * 2);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.sdiff__sum {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.sdiff__add {
  color: var(--omo-ok);
}
.sdiff__del {
  color: var(--omo-danger);
}
.sdiff__note {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.sdiff__list {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
  padding: 0;
  list-style: none;
}
</style>
