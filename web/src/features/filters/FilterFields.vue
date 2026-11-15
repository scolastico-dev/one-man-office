<script setup lang="ts">
// The selects and the "Needs me" toggle of the filters, shown in the top bar
// and, below 768 px, in the filter sheet.
import { computed } from 'vue'
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

const stories = computed(() => {
  const list: Array<{ number: number; title: string }> = []
  for (const { t } of store.state.tickets.values()) {
    if (t.type === 'story' && t.column !== 'done') list.push({ number: t.number, title: t.title })
  }
  const current = filters.value.story
  if (current !== undefined && !list.some((s) => s.number === current)) {
    list.push({ number: current, title: store.view(current)?.title ?? '' })
  }
  return list.sort((a, b) => a.number - b.number)
})

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
const onInput = (key: 'project' | 'story' | 'supervisor') => (e: Event) => set({ [key]: (e.target as HTMLSelectElement).value })
</script>

<template>
  <div class="fields" :class="`fields--${layout ?? 'bar'}`">
    <label class="fields__item">
      <span class="fields__label" :class="{ 'sr-only': layout !== 'sheet' }">Project</span>
      <select class="omo-input fields__control" :value="filters.project ?? ''" @change="onInput('project')($event)">
        <option value="">All projects</option>
        <option v-for="p in projects" :key="p.id" :value="p.id">{{ p.name }}</option>
      </select>
    </label>
    <label class="fields__item">
      <span class="fields__label" :class="{ 'sr-only': layout !== 'sheet' }">Story</span>
      <select class="omo-input fields__control" :value="filters.story === undefined ? '' : String(filters.story)" @change="onInput('story')($event)">
        <option value="">All stories</option>
        <option v-for="s in stories" :key="s.number" :value="String(s.number)">{{ `#${s.number} ${s.title}` }}</option>
      </select>
    </label>
    <label class="fields__item">
      <span class="fields__label" :class="{ 'sr-only': layout !== 'sheet' }">Supervisor</span>
      <select class="omo-input fields__control" :value="filters.supervisor ?? ''" @change="onInput('supervisor')($event)">
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
.fields__control {
  --omo-input-pad-x: calc(var(--omo-space) * 2);
  min-height: calc(var(--omo-space) * 8);
  padding-block: calc(var(--omo-space) * 1);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
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
</style>
