<script setup lang="ts">
// The tickets that wait for this one: read-only links that open them.
import { RouterLink, useRoute } from 'vue-router'
import type { TicketSummary } from '../../../api/types'
import { useBoard } from '../../../store'
import { COLUMN_TITLES } from '../../board/model'
import FieldRow from '../FieldRow.vue'

defineProps<{ ticket: TicketSummary }>()
const store = useBoard()
const route = useRoute()
const info = (n: number) => {
  const t = store.view(n)
  return { n, text: t ? `#${n} ${t.title}` : `#${n}`, column: t ? COLUMN_TITLES[t.column] : '' }
}
</script>

<template>
  <FieldRow field="blocks" label="Blocks" :editable="false" :editing="false">
    <template #value>
      <span v-if="ticket.blocks.length === 0">Nothing waits for this ticket.</span>
      <span v-for="b in ticket.blocks.map(info)" v-else :key="b.n" class="dep">
        <RouterLink class="dep__link" :to="{ path: `/${b.n}`, query: route.query }">{{ b.text }}</RouterLink>
        <span v-if="b.column" class="dep__column">{{ b.column }}</span>
      </span>
    </template>
  </FieldRow>
</template>

<style scoped>
.dep {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
}
.dep__link {
  color: var(--omo-accent);
}
.dep__column {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
