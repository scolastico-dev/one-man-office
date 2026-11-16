<script setup lang="ts">
// Chooses the project of a task (one) or the projects of a story (any number)
// from the known projects, and adds a new one without leaving the dialog.
// With more than FILTER_FROM projects a filter field narrows the list (by
// name and folder); the chosen projects stay in the list while filtering.
import { computed, ref, useId } from 'vue'
import type { Project } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import { filterPicks } from '../../components/ui/searchPick'
import { useBoard } from '../../store'
import AddProjectDialog from '../projects/AddProjectDialog.vue'
import { projectState } from '../projects/projectState'
import { branchNotice } from './branchNotice'

const props = withDefaults(
  defineProps<{
    label: string
    /** One project (radio buttons) or several (check boxes). */
    multiple: boolean
    hint?: string
    error?: string
    /** Only these project ids can be chosen (the projects of the task's story); null means all. */
    only?: string[] | null
    /** Whether a project on a branch other than its default carries a notice; a task in a story is based on its story's branch. */
    notice?: boolean
    /** Projects the ticket already has: they keep their base branch, so there is nothing to warn about. */
    existing?: string[]
  }>(),
  { hint: undefined, error: undefined, only: null, notice: true, existing: () => [] },
)

const model = defineModel<string[]>({ required: true })
const emit = defineEmits<{ added: [project: Project] }>()

const store = useBoard()
const base = useId()
const errorId = `${base}-error`
const hintId = `${base}-hint`
const adding = ref(false)
/** Projects added here, shown until the event stream has told the store about them. */
const fresh = ref<Project[]>([])

const known = computed<Project[]>(() => {
  const byId = new Map<string, Project>()
  for (const p of fresh.value) byId.set(p.id, p)
  for (const p of store.state.projects.values()) byId.set(p.id, p)
  return [...byId.values()]
})

/** Enabled projects, and missing ones (disabled, with their reason); switched-off projects are not offered. */
const options = computed(() =>
  known.value
    .filter((p) => (props.only === null || props.only.includes(p.id)) && (p.enabled || p.missing))
    .sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id)),
)

/** More projects than this and a filter field is shown above the list. */
const FILTER_FROM = 8
const filterId = `${base}-filter`
const countId = `${base}-count`
const query = ref('')
const filterable = computed(() => options.value.length > FILTER_FROM)
const shown = computed(() => {
  if (!filterable.value) return options.value
  const picks = options.value.map((p) => ({ id: p.id, label: p.name, detail: p.path, always: model.value.includes(p.id), project: p }))
  return filterPicks(picks, query.value).map((x) => x.project)
})
const countText = computed(() =>
  query.value.trim() === '' ? `${options.value.length} projects` : `${shown.value.filter((p) => !model.value.includes(p.id)).length} of ${options.value.length} projects match`,
)

function toggle(id: string, on: boolean) {
  if (!props.multiple) model.value = on ? [id] : []
  else model.value = on ? [...model.value.filter((x) => x !== id), id] : model.value.filter((x) => x !== id)
}

function added(p: Project) {
  fresh.value.push(p)
  toggle(p.id, true)
  emit('added', p)
}
</script>

<template>
  <fieldset class="picker" :aria-describedby="[hint ? hintId : '', error ? errorId : ''].filter(Boolean).join(' ') || undefined">
    <legend class="omo-field__label">{{ label }}</legend>
    <p v-if="options.length === 0" class="picker__none">
      {{ only !== null ? 'The story has no project you can use.' : 'No project yet. Add the git repository the work happens in.' }}
    </p>
    <div v-if="filterable" class="picker__filter">
      <label :for="filterId" class="sr-only">Filter projects</label>
      <input
        :id="filterId"
        v-model="query"
        type="search"
        class="omo-input"
        placeholder="Filter projects"
        autocomplete="off"
        spellcheck="false"
        :aria-describedby="countId"
        data-project-filter
        @keydown.enter.prevent
      />
      <p :id="countId" class="picker__count" role="status" data-project-count>{{ countText }}</p>
    </div>
    <ul v-if="options.length > 0" class="picker__list" role="list">
      <li v-for="p in shown" :key="p.id">
        <label class="picker__option" :class="{ 'picker__option--off': p.missing }">
          <input
            :type="multiple ? 'checkbox' : 'radio'"
            :name="`${base}-project`"
            :value="p.id"
            :checked="model.includes(p.id)"
            :disabled="p.missing"
            @change="toggle(p.id, ($event.target as HTMLInputElement).checked)"
          />
          <span class="picker__name">{{ p.name }}</span>
          <span class="picker__path">{{ p.path }}</span>
          <span v-if="projectState(p) === 'missing'" class="picker__state">missing</span>
        </label>
        <p v-if="notice && model.includes(p.id) && !existing.includes(p.id) && branchNotice(p)" class="omo-field__hint picker__notice" data-branch-notice>
          {{ p.name }} is on <code>{{ p.current_branch }}</code>, not its default branch <code>{{ p.default_branch }}</code>. The ticket will be based on
          <code>{{ p.current_branch }}</code>.
        </p>
      </li>
    </ul>
    <p v-if="filterable && shown.length === 0" class="picker__none" data-project-none>No project matches the filter.</p>
    <div v-if="only === null"><UiButton size="sm" data-add-project @click="adding = true">Add project…</UiButton></div>
    <p v-if="hint" :id="hintId" class="omo-field__hint">{{ hint }}</p>
    <p v-if="error" :id="errorId" class="omo-field__error">{{ error }}</p>
    <AddProjectDialog :open="adding" @close="adding = false" @added="added" />
  </fieldset>
</template>

<style scoped>
.picker {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}
.picker__list {
  display: grid;
  gap: var(--omo-space);
  margin: 0;
  padding: 0;
  list-style: none;
}
.picker__option {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--omo-space) calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 2);
  border: var(--omo-border) solid var(--omo-line);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.picker__option:has(input:checked) {
  border-color: var(--omo-accent);
  background: var(--omo-accent-soft);
}
.picker__option--off {
  color: var(--omo-muted);
  cursor: not-allowed;
}
.picker__name {
  font-weight: 700;
}
.picker__path {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.picker__state {
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
}
.picker__notice {
  margin-top: var(--omo-space);
}
.picker__filter {
  display: grid;
  gap: var(--omo-space);
}
.picker__count {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.picker__none {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
