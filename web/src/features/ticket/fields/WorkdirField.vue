<script setup lang="ts">
// The working folder of a task without a project, in place of its branch:
// the folder omo made for the agent's work (notes, a proof of concept). It
// stays after the task is done, until the user removes it here ("Remove
// folder", after a confirmation). Open terminal (an action of the ticket)
// opens a shell in it.
import { ref } from 'vue'
import type { TicketSummary } from '../../../api/types'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import FieldRow from '../FieldRow.vue'
import RemoveWorkdirDialog from '../RemoveWorkdirDialog.vue'

defineProps<{ ticket: TicketSummary }>()
const removing = ref(false)
</script>

<template>
  <FieldRow field="workdir" label="Working folder" :editable="false" :editing="false">
    <template #value>
      <span class="workdir__line">
        <code v-if="ticket.workdir !== ''" class="workdir__path" data-workdir-path>{{ visibleText(ticket.workdir) }}</code>
        <span v-else-if="ticket.column === 'done'" data-workdir-none>Removed, or never made</span>
        <span v-else data-workdir-none>Made when work starts</span>
        <span v-if="ticket.workdir !== '' && ticket.column === 'done'" class="workdir__hint">Kept until you remove it.</span>
        <span v-if="ticket.workdir !== '' && ticket.column === 'done'" class="workdir__remove">
          <UiButton size="sm" variant="ghost" data-remove-workdir @click="removing = true">Remove folder</UiButton>
        </span>
      </span>
      <RemoveWorkdirDialog v-if="removing" :open="removing" :ticket="ticket" @close="removing = false" />
    </template>
  </FieldRow>
</template>

<style scoped>
.workdir__line {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
}
.workdir__path {
  font: inherit;
  overflow-wrap: anywhere;
}
.workdir__hint {
  color: var(--omo-muted);
}
.workdir__remove {
  justify-self: start;
}
</style>
