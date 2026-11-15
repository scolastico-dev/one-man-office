<script setup lang="ts">
import type { TicketSummary } from '../../../api/types'
import { useBoard } from '../../../store'
import FieldRow from '../FieldRow.vue'

defineProps<{ ticket: TicketSummary }>()
const store = useBoard()
const nameOf = (id: string) => store.state.projects.get(id)?.name ?? id
const safe = (url: string | undefined) => (url !== undefined && /^https?:\/\//iu.test(url) ? url : '')
</script>

<template>
  <FieldRow v-if="ticket.projects.length > 0" field="branch" :label="ticket.projects.length > 1 ? 'Branches' : 'Branch'" :editable="false" :editing="false">
    <template #value>
      <span v-for="p in ticket.projects" :key="p.project" class="branch__line" :data-branch="p.project">
        <span v-if="ticket.projects.length > 1" class="branch__project">{{ nameOf(p.project) }}</span>
        <code v-if="p.branch !== ''" class="branch__name">{{ p.branch }}</code>
        <span v-else>Not created yet</span>
        <a v-if="safe(p.export_url)" :href="safe(p.export_url)" class="branch__pr" target="_blank" rel="noreferrer noopener">Pull request{{ ticket.projects.length > 1 ? ` for ${nameOf(p.project)}` : '' }}</a>
      </span>
    </template>
  </FieldRow>
</template>

<style scoped>
.branch__line {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
}
.branch__project {
  color: var(--omo-muted);
}
.branch__name {
  font: inherit;
  overflow-wrap: anywhere;
}
.branch__pr {
  color: var(--omo-accent);
}
</style>
