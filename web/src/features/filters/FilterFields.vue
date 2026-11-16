<script setup lang="ts">
// The filters' controls and the "Needs me" toggle, shown in the top bar and,
// below 768 px, in the filter sheet. Project and story are chosen with a
// search (both lists grow with the data); "All projects" and "All stories" are
// always first. The supervisor's short, fixed list stays a select.
import { computed } from 'vue'
import SearchPicker, { type PickItem } from '../../components/ui/SearchPicker.vue'
import { visibleText } from '../../components/ui/visibleText'
import { COLUMN_TITLES } from '../board/model'
import { useBoard } from '../../store'
import { SUPERVISOR_FILTERS, useFilters } from './useFilters'

defineProps<{ layout?: 'bar' | 'sheet' }>()

const store = useBoard()
const { filters, needsMeCount } = useFilters()

const projects = computed(() => {
  const list = [...store.state.projects.values()].filter((p) => p.enabled).map((p) => ({ id: p.id, name: p.name }))
  const current = filters.value.project
  if (current !== undefined && !list.some((p) => p.id === current)) list.push({ id: current, name: current })
  return list.sort((a, b) => a.name.localeCompare(b.name))
})

const ALL_PROJECTS = 'All projects'
const projectPicks = computed<PickItem[]>(() => [
  { id: '', label: ALL_PROJECTS, always: true },
  ...projects.value.map((p) => ({ id: p.id, label: visibleText(p.name) })),
])
const projectShown = computed(() => {
  const id = filters.value.project
  if (id === undefined) return ALL_PROJECTS
  return visibleText(projects.value.find((p) => p.id === id)?.name ?? id)
})
const projectCount = (matching: number, all: number) => (matching === all ? `${all} projects` : `${matching} of ${all} projects`)

const stories = computed(() => {
  const list: Array<{ number: number; title: string; column?: string }> = []
  for (const { t } of store.state.tickets.values()) {
    if (t.type === 'story' && t.column !== 'done') list.push({ number: t.number, title: t.title, column: COLUMN_TITLES[t.column] })
  }
  const current = filters.value.story
  if (current !== undefined && !list.some((s) => s.number === current)) {
    const t = store.view(current)
    list.push({ number: current, title: t?.title ?? '', column: t ? COLUMN_TITLES[t.column] : undefined })
  }
  return list.sort((a, b) => a.number - b.number)
})

const ALL_STORIES = 'All stories'
const storyLabel = (s: { number: number; title: string }) => (s.title === '' ? `#${s.number}` : `#${s.number} ${visibleText(s.title)}`)
const storyPicks = computed<PickItem[]>(() => [
  { id: '', label: ALL_STORIES, always: true },
  ...stories.value.map((s) => ({ id: String(s.number), label: storyLabel(s), detail: s.column, detailSearched: false })),
])
const storyShown = computed(() => {
  const n = filters.value.story
  if (n === undefined) return ALL_STORIES
  return storyLabel(stories.value.find((s) => s.number === n) ?? { number: n, title: '' })
})
const storyCount = (matching: number, all: number) => (matching === all ? `${all} open ${all === 1 ? 'story' : 'stories'}` : `${matching} of ${all} open stories`)

function set(patch: Partial<Record<'project' | 'story' | 'supervisor', string>>) {
  const f = { ...filters.value }
  if ('project' in patch) {
    if (patch.project) f.project = patch.project
    else delete f.project
  }
  if ('story' in patch) {
    if (patch.story) f.story = Number(patch.story)
    else delete f.story
  }
  if ('supervisor' in patch) {
    const role = SUPERVISOR_FILTERS.find((s) => s.role === patch.supervisor)
    if (role) f.supervisor = role.role
    else delete f.supervisor
  }
  filters.value = f
}

function toggleNeedsMe() {
  const f = { ...filters.value }
  if (f.needsMe === true) delete f.needsMe
  else f.needsMe = true
  filters.value = f
}
const onInput = (key: 'supervisor') => (e: Event) => set({ [key]: (e.target as HTMLSelectElement).value })
</script>

<template>
  <div class="fields" :class="`fields--${layout ?? 'bar'}`">
    <div class="fields__item" data-filter-project>
      <span v-if="layout === 'sheet'" class="fields__label" aria-hidden="true">Project</span>
      <SearchPicker
        class="fields__pick"
        :label="projectShown"
        :button-name="`Project: ${projectShown}`"
        search-label="Search projects"
        :items="projectPicks"
        :count-text="projectCount"
        size="sm"
        empty="No project matches."
        @select="(id) => set({ project: id })"
      />
    </div>
    <div class="fields__item" data-filter-story>
      <span v-if="layout === 'sheet'" class="fields__label" aria-hidden="true">Story</span>
      <SearchPicker
        class="fields__pick"
        :label="storyShown"
        :button-name="`Story: ${storyShown}`"
        search-label="Search stories"
        :items="storyPicks"
        :count-text="storyCount"
        size="sm"
        empty="No open story matches."
        @select="(id) => set({ story: id })"
      />
    </div>
    <label class="fields__item">
      <span class="fields__label" :class="{ 'sr-only': layout !== 'sheet' }">Supervisor</span>
      <select class="omo-input omo-input--sm fields__control" :value="filters.supervisor ?? ''" @change="onInput('supervisor')($event)">
        <option value="">Anyone</option>
        <option v-for="s in SUPERVISOR_FILTERS" :key="s.role" :value="s.role">{{ s.label }}</option>
      </select>
    </label>
    <button type="button" class="omo-btn fields__needs" :aria-pressed="filters.needsMe === true" @click="toggleNeedsMe">
      Needs me <span data-needs-me-count>{{ needsMeCount }}</span>
    </button>
  </div>
</template>

<style scoped>
.fields {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
/* In the bar the controls sit directly in the form's row. */
.fields--bar {
  display: contents;
}
.fields--bar .fields__control {
  width: auto;
  max-width: calc(var(--omo-space) * 40);
}
.fields--sheet {
  flex-direction: column;
  align-items: stretch;
  gap: calc(var(--omo-space) * 4);
}
.fields--sheet .fields__item {
  display: grid;
  gap: var(--omo-space);
}
.fields--sheet .fields__label {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.fields--sheet .fields__control {
  width: 100%;
}
.fields--sheet .fields__pick,
.fields--sheet .fields__pick :deep(.omo-btn) {
  width: 100%;
}
.fields__pick :deep(.omo-btn) {
  max-width: calc(var(--omo-space) * 40);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.fields--sheet .fields__pick :deep(.omo-btn) {
  max-width: none;
}
</style>
