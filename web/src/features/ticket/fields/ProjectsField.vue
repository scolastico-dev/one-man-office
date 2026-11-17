<script setup lang="ts">
import { computed } from 'vue'
import type { TicketSummary } from '../../../api/types'
import { useBoard } from '../../../store'
import ProjectPicker from '../../create/ProjectPicker.vue'
import FieldRow from '../FieldRow.vue'
import { inStory, type FieldState } from '../fieldRules'
import { useFieldEdit } from '../useFieldEdit'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()
const store = useBoard()

const isTask = computed(() => props.ticket.type === 'task')
const label = computed(() => (isTask.value ? 'Project' : 'Projects'))
const ids = (t: TicketSummary) => t.projects.map((p) => p.project)
const nameOf = (id: string) => store.state.projects.get(id)?.name ?? id
const words = (v: string[]) => (v.length === 0 ? (isTask.value ? 'no project' : 'none') : v.map(nameOf).join(', '))

const edit = useFieldEdit<string[]>({
  number: () => props.ticket.number,
  current: () => ids(props.ticket),
  // A task without a project has no "when approved"; its own setting goes with the project.
  patch: (v) => (isTask.value && v.length === 0 && props.ticket.when_approved !== null ? { projects: v, when_approved: null } : { projects: v }),
  text: words,
  label: 'Project',
})

/** A task in a story uses one of the story's projects. */
const only = computed(() => {
  const t = props.ticket
  if (!inStory(t) || t.story === null) return null
  const s = store.view(t.story)
  return s ? s.projects.map((p) => p.project) : null
})
</script>

<template>
  <FieldRow
    field="projects"
    :label="label"
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
      <span v-if="edit.shown.value.length === 0">{{ isTask ? 'No project' : 'None' }}</span>
      <span v-for="id in edit.shown.value" v-else :key="id" :title="store.state.projects.get(id)?.path">{{ nameOf(id) }}</span>
    </template>
    <template #edit>
      <ProjectPicker
        v-model="edit.draft.value"
        :none="isTask && edit.draft.value.length === 0"
        :label="label"
        :multiple="!isTask"
        :none-option="isTask"
        :only="only"
        :notice="!inStory(ticket)"
        :existing="ids(ticket)"
        :hint="isTask ? (edit.draft.value.length === 0 ? 'Without a project the task works in a folder of its own and changes no repository.' : undefined) : 'Projects the story\'s tasks can use.'"
      />
    </template>
  </FieldRow>
</template>
