<script setup lang="ts">
import { computed } from 'vue'
import type { TicketSummary } from '../../../api/types'
import { useBoard } from '../../../store'
import FieldRow from '../FieldRow.vue'
import type { FieldState } from '../fieldRules'
import { useFieldEdit } from '../useFieldEdit'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()
const store = useBoard()

type Bases = Record<string, string>
const bases = (t: TicketSummary): Bases => Object.fromEntries(t.projects.map((p) => [p.project, p.base_branch]))
const nameOf = (id: string) => store.state.projects.get(id)?.name ?? id
const line = (id: string, branch: string) => `${nameOf(id)}: ${branch === '' ? 'not set' : branch}`
const words = (v: Bases) => Object.entries(v).map(([id, b]) => line(id, b)).join(', ')

const edit = useFieldEdit<Bases>({
  number: () => props.ticket.number,
  current: () => bases(props.ticket),
  // Only the branches that changed are sent.
  patch: (v) => {
    const now = bases(props.ticket)
    return { base_branches: Object.fromEntries(Object.entries(v).filter(([id, b]) => b.trim() !== (now[id] ?? ''))) }
  },
  text: words,
  label: 'Base branch',
})

const entries = computed(() => Object.keys(edit.shown.value))
</script>

<template>
  <FieldRow
    v-if="ticket.projects.length > 0"
    field="base_branch"
    :label="ticket.projects.length > 1 ? 'Base branches' : 'Base branch'"
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
      <span v-for="id in entries" :key="id">{{ ticket.projects.length > 1 ? line(id, edit.shown.value[id] ?? '') : edit.shown.value[id] || 'Not set' }}</span>
    </template>
    <template #edit>
      <label v-for="id in Object.keys(edit.draft.value)" :key="id" class="omo-field">
        <span class="omo-field__label">Base branch of {{ nameOf(id) }}</span>
        <input v-model="edit.draft.value[id]" class="omo-input" autocomplete="off" spellcheck="false" />
      </label>
      <p class="omo-field__hint">The branch the work starts from and is merged into.</p>
    </template>
  </FieldRow>
</template>
