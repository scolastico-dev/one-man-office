<script setup lang="ts">
// The story a task belongs to. Moving a task into, out of or between stories
// changes more than this field, so the editor says what else changes before
// anything is sent, and asks for the project when the new story does not use
// the task's own.
import { computed, watch } from 'vue'
import type { TicketSummary } from '../../../api/types'
import { useBoard } from '../../../store'
import { COLUMN_TITLES } from '../../board/model'
import { storiesForTasks } from '../../create/createRules'
import ProjectPicker from '../../create/ProjectPicker.vue'
import FieldRow from '../FieldRow.vue'
import type { FieldState } from '../fieldRules'
import { useFieldEdit } from '../useFieldEdit'
import TicketLink from '../TicketLink.vue'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()
const store = useBoard()

interface Draft {
  story: number | null
  projects: string[]
}

const projectIds = () => props.ticket.projects.map((p) => p.project)
const storyText = (n: number | null) => {
  if (n === null) return 'no story, a standalone task'
  const s = store.view(n)
  return s ? `#${n} ${s.title}` : `#${n}`
}

const edit = useFieldEdit<Draft>({
  number: () => props.ticket.number,
  current: () => ({ story: props.ticket.story, projects: projectIds() }),
  patch: (d) => {
    const patch: { story: number | null; projects?: string[] } = { story: d.story }
    if (d.story !== null && d.projects.join() !== projectIds().join()) patch.projects = d.projects
    return patch
  },
  text: (d) => storyText(d.story),
  label: 'Story',
  // Choosing the story it is in already changes nothing, whatever the project picker says.
  equals: (a, b) => a.story === b.story,
})

const stories = computed(() => {
  const list = storiesForTasks([...store.state.tickets.values()].map((e) => e.t))
  const cur = props.ticket.story
  const now = cur === null ? undefined : store.view(cur)
  if (now && !list.some((s) => s.number === now.number)) list.push(now)
  return list.sort((a, b) => a.number - b.number)
})
const chosen = computed(() => (edit.draft.value.story === null ? undefined : store.view(edit.draft.value.story)))
const storyProjects = computed(() => (chosen.value ? chosen.value.projects.map((p) => p.project) : null))
/** The project must be one of the new story's projects; ask when the task's own is not. */
const needsProject = computed(() => storyProjects.value !== null && !projectIds().every((id) => storyProjects.value?.includes(id)))
/** The server refuses a move into a story while the task's own setting is export. */
const exportBlocks = computed(() => edit.draft.value.story !== null && props.ticket.when_approved === 'export')
const selectValue = computed({
  get: () => (edit.draft.value.story === null ? '' : String(edit.draft.value.story)),
  set: (v: string) => (edit.draft.value = { ...edit.draft.value, story: v === '' ? null : Number(v) }),
})
const projects = computed({
  get: () => edit.draft.value.projects,
  set: (v: string[]) => (edit.draft.value = { ...edit.draft.value, projects: v }),
})

// A new story: keep the task's project when the story uses it, else the story's only project, else ask.
// A task without a project stays without one.
watch(
  () => edit.draft.value.story,
  () => {
    if (!edit.editing.value) return
    const allowed = storyProjects.value
    if (allowed === null || projectIds().length === 0) {
      projects.value = projectIds()
      return
    }
    const own = projectIds().filter((id) => allowed.includes(id))
    projects.value = own.length > 0 ? own : allowed.length === 1 ? allowed.slice() : []
  },
)

const columnOf = (n: number) => {
  const s = store.view(n)
  return s ? COLUMN_TITLES[s.column] : undefined
}
</script>

<template>
  <FieldRow
    field="story"
    label="Story"
    :clickable="false"
    edit-label="Change story"
    :editable="state.editable"
    :locked-reason="state.lockedReason"
    :editing="edit.editing.value"
    :saving="edit.saving.value"
    :error="edit.error.value"
    :changed-note="edit.changedNote.value"
    @edit="edit.start"
    @save="edit.save"
    @cancel="edit.cancel"
    @use-new="edit.useNew"
  >
    <template #value>
      <template v-if="edit.shown.value.story === null">None, a standalone task</template>
      <TicketLink v-else class="story__link" :number="edit.shown.value.story" :title="columnOf(edit.shown.value.story)">{{ storyText(edit.shown.value.story) }}</TicketLink>
    </template>
    <template #edit="{ labelId }">
      <select v-model="selectValue" class="omo-input" :aria-labelledby="labelId">
        <option value="">No story: a standalone task</option>
        <option v-for="s in stories" :key="s.number" :value="String(s.number)">#{{ s.number }} {{ s.title }} ({{ COLUMN_TITLES[s.column] }})</option>
      </select>
      <p class="omo-field__hint" data-move-note>
        Moving a task between stories, or into or out of one, changes its supervisor to the new default. A task in a story uses one of the story's projects; a task outside any story has a base branch of its own.
      </p>
      <p v-if="exportBlocks" class="omo-field__hint" data-export-note>
        This task is set to export as a pull request, which a task in a story cannot do. Set When approved to Default or Merge locally first, or omo refuses the move.
      </p>
      <ProjectPicker v-if="needsProject && chosen" v-model="projects" label="Project in the new story" :multiple="false" :only="storyProjects" :hint="`The task's project is not one of #${chosen.number}'s projects. Choose one that is.`" />
    </template>
  </FieldRow>
</template>

<style scoped>
.story__link {
  color: var(--omo-accent);
}
</style>
