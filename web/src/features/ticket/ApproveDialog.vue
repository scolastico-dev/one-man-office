<script setup lang="ts">
// Confirms approving a review that waits for the user, and says what omo does
// next: a local merge or an export, as the ticket's setting asks.
import { computed } from 'vue'
import type { TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { approvalEffect } from './actions'

const props = withDefaults(
  defineProps<{
    open: boolean
    ticket: TicketSummary
    busy?: boolean
    error?: string
  }>(),
  { busy: false, error: '' },
)
const emit = defineEmits<{ close: []; confirm: [] }>()

const exporting = computed(() => props.ticket.effective_when_approved === 'export')
</script>

<template>
  <UiDialog :open="open" :title="`Approve #${ticket.number}?`" :eyebrow="ticket.title" @close="emit('close')">
    <div class="approve" data-approve-dialog>
      <p class="approve__text" data-approve-effect>{{ approvalEffect(ticket) }}</p>
      <p class="approve__text approve__muted">
        The card stays in Review, marked {{ exporting ? 'exporting…' : 'merging…' }}, until omo has finished.
      </p>
      <p v-if="error" class="approve__error" role="alert" data-action-error>{{ error }}</p>
    </div>
    <template #actions>
      <UiButton @click="emit('close')">Not yet</UiButton>
      <UiButton variant="primary" :disabled="busy" @click="!busy && emit('confirm')">
        {{ exporting ? 'Approve and export' : 'Approve and merge' }}
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.approve {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.approve__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.approve__muted {
  color: var(--omo-muted);
}
.approve__error {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
