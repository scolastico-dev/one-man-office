<script setup lang="ts">
// The ticket's description, in the panel and on the ticket's page: the
// rendered text with History and Edit, or the editor in its place, and the
// history dialog.
import type { TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import DescriptionEditor from './DescriptionEditor.vue'
import DescriptionHistoryDialog from './DescriptionHistoryDialog.vue'
import DescriptionView from './DescriptionView.vue'
import type { TicketView } from './useTicketView'

defineProps<{
  view: TicketView
  ticket: TicketSummary
  /** Id of the section's heading. */
  headingId: string
}>()
</script>

<template>
  <section class="tdesc" :aria-labelledby="headingId" data-ticket-description>
    <div class="tdesc__head">
      <h3 :id="headingId" class="tdesc__h3">Description</h3>
      <div class="tdesc__row">
        <UiButton v-if="view.desc.value !== null" size="sm" data-open-history @click="view.historyOpen.value = true">
          History ({{ ticket.description_version }})
        </UiButton>
        <UiButton
          v-if="!view.editing.value && !view.readonly.value && view.desc.value !== null"
          size="sm"
          data-edit-description
          @click="view.startEditing()"
          >Edit</UiButton
        >
      </div>
    </div>
    <DescriptionEditor
      v-if="view.editing.value && view.desc.value !== null"
      :ticket="ticket"
      :current="view.desc.value"
      @close="view.stopEditing()"
      @saved="view.onSaved"
      @dirty="view.descDirty.value = $event"
      @refresh="view.load()"
    />
    <template v-else>
      <div v-if="view.desc.value === null && view.loadState.value === 'failed'" class="tdesc__row" role="alert">
        <p class="tdesc__text">{{ view.loadError.value }}</p>
        <UiButton size="sm" @click="view.load()">Try again</UiButton>
      </div>
      <p v-else-if="view.desc.value === null" class="tdesc__text tdesc__muted">{{ view.slow.value ? 'Loading the description…' : '' }}</p>
      <DescriptionView v-else-if="view.shownText.value.trim() !== ''" :text="view.shownText.value" />
      <p v-else class="tdesc__text tdesc__muted" data-description-empty>No description yet.</p>
    </template>
    <DescriptionHistoryDialog
      v-if="view.historyOpen.value"
      open
      :ticket="ticket"
      :editing="view.editing.value"
      @close="view.historyOpen.value = false"
      @restored="view.onRestored"
    />
  </section>
</template>

<style scoped>
.tdesc {
  min-width: 0;
}
.tdesc__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  min-height: calc(var(--omo-space) * 7);
  margin-bottom: calc(var(--omo-space) * 3);
}
.tdesc__h3 {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.tdesc__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.tdesc__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.tdesc__muted {
  color: var(--omo-muted);
}
</style>
