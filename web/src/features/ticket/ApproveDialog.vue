<script setup lang="ts">
// Confirms approving a review that waits for the user, and says what omo does
// next: a local merge or an export, as the ticket's setting asks. In export
// mode (the Export… action) it exports whatever the setting says. When the
// setting is export, "Merge locally instead" approves with a local merge (of
// the projects without a pull request, after an export that failed in part).
import { computed } from 'vue'
import type { TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useBoard } from '../../store'
import { approvalEffect, projectNames } from './actions'
import { exportEffect, mergeInsteadNote } from './exportState'

const props = withDefaults(
  defineProps<{
    open: boolean
    ticket: TicketSummary
    busy?: boolean
    error?: string
    /** `export` exports regardless of the ticket's setting. */
    mode?: 'default' | 'export'
  }>(),
  { busy: false, error: '', mode: 'default' },
)
/** `confirm` carries nothing for the setting's own way, else what to send. */
const emit = defineEmits<{ close: []; confirm: [opts?: { export?: boolean; merge?: boolean }] }>()

const nameOf = projectNames(useBoard())
const forced = computed(() => props.mode === 'export')
const exporting = computed(() => forced.value || props.ticket.effective_when_approved === 'export')
const effect = computed(() => (forced.value ? exportEffect(props.ticket, nameOf) : approvalEffect(props.ticket, nameOf)))
const mergeNote = computed(() => (!forced.value && exporting.value ? mergeInsteadNote(props.ticket, nameOf) : ''))
</script>

<template>
  <UiDialog :open="open" :title="forced ? `Export #${ticket.number}?` : `Approve #${ticket.number}?`" :eyebrow="ticket.title" @close="emit('close')">
    <div class="approve" data-approve-dialog>
      <p class="approve__text" data-approve-effect>{{ effect }}</p>
      <p v-if="mergeNote" class="approve__text" data-merge-instead-note>{{ mergeNote }}</p>
      <p class="approve__text approve__muted">
        The card stays in Review, marked {{ exporting ? 'exporting…' : 'merging…' }}, until omo has finished.
      </p>
      <p v-if="error" class="approve__error" role="alert" data-action-error>{{ error }}</p>
    </div>
    <template #actions>
      <UiButton @click="emit('close')">Not yet</UiButton>
      <UiButton
        v-if="!forced && exporting"
        :disabled="busy"
        data-merge-instead
        @click="!busy && emit('confirm', { merge: true })"
      >
        Merge locally instead
      </UiButton>
      <UiButton
        variant="primary"
        :disabled="busy"
        @click="!busy && (forced ? emit('confirm', { export: true }) : emit('confirm'))"
      >
        {{ forced ? 'Export' : exporting ? 'Approve and export' : 'Approve and merge' }}
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
