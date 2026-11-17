<script setup lang="ts">
// Confirms stopping a ticket by dragging it from In Progress to Planning or
// Ready For Dev, and names the agents that end with it (for a story: its
// product manager and the agents of its tasks). Nothing is sent before.
import { computed } from 'vue'
import type { Role, TicketSummary } from '../../../api/types'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'
import { COLUMN_TITLES, ROLE_LABELS } from '../model'

const props = withDefaults(
  defineProps<{
    open: boolean
    ticket: TicketSummary
    to: 'planning' | 'ready'
    /** The running agents that stop with it (a story's tasks included). */
    agents: { ticket: number; role: Role; name: string }[]
    /** The stop was sent and has no answer yet. */
    busy?: boolean
    /** Why omo refused to stop it. */
    error?: string
  }>(),
  { busy: false, error: '' },
)
const emit = defineEmits<{ close: []; confirm: [] }>()

const story = computed(() => props.ticket.type === 'story')
</script>

<template>
  <UiDialog
    :open="open"
    :title="`Stop #${ticket.number} and move it to ${COLUMN_TITLES[to]}?`"
    :eyebrow="ticket.title"
    @close="emit('close')"
  >
    <div class="stop">
      <template v-if="agents.length > 0">
        <p class="stop__text">The agent stops. The branch and worktree are kept.</p>
        <p class="stop__text">{{ story ? 'This also stops:' : 'Agents that stop:' }}</p>
        <ul class="stop__agents" data-stop-agents>
          <li v-for="a in agents" :key="`${a.ticket}-${a.role}-${a.name}`">
            #{{ a.ticket }} {{ ROLE_LABELS[a.role] }} · {{ a.name }}
          </li>
        </ul>
      </template>
      <template v-else>
        <p class="stop__text">The branch and worktree are kept.</p>
        <p class="stop__text stop__muted" data-stop-no-agents>No agent is working on it.</p>
      </template>
      <p v-if="story" class="stop__text">Its tasks in In Progress or Review return to Ready For Dev.</p>
      <p v-if="error" class="stop__error" role="alert" data-move-error>{{ error }}</p>
    </div>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton variant="danger" :disabled="busy" data-primary @click="emit('confirm')">Stop and move</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.stop {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.stop__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.stop__muted {
  color: var(--omo-muted);
}
.stop__agents {
  margin: 0;
  padding-left: calc(var(--omo-space) * 5);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.stop__error {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
