<script setup lang="ts">
// Confirms removing the working folder of a done task without a project:
// the folder and everything the agent left in it are deleted.
import { ref, watch } from 'vue'
import { removeTicketWorkdir } from '../../api/endpoints'
import { failureText } from '../../api/lostAnswer'
import type { TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'

const props = defineProps<{ open: boolean; ticket: TicketSummary }>()
const emit = defineEmits<{ close: [] }>()

const session = useSession()
const busy = ref(false)
const error = ref('')

watch(
  () => props.open,
  (isOpen) => {
    if (!isOpen) return
    busy.value = false
    error.value = ''
  },
  { immediate: true },
)

async function submit() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await removeTicketWorkdir(session.client, props.ticket.number)
    emit('close')
  } catch (err) {
    error.value = failureText(err, {
      lost: 'No answer from omo, so the folder may have been removed. The ticket shows whether it is still there.',
      refused: 'omo refused it.',
    })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiDialog :open="open" :title="`Remove the working folder of #${ticket.number}?`" :eyebrow="ticket.title" @close="emit('close')">
    <form id="remove-workdir-form" class="remove" novalidate data-remove-workdir-dialog @submit.prevent="submit">
      <p class="remove__text">
        omo deletes <code>{{ visibleText(ticket.workdir) }}</code> with everything in it: what the agent left there, a proof of concept, notes. The ticket and its comments stay.
      </p>
      <p class="remove__text remove__muted">While a terminal is open in the folder, omo refuses; close it first.</p>
      <p v-if="error" class="remove__error" role="alert" data-action-error>{{ error }}</p>
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Keep the folder</UiButton>
      <UiButton type="submit" form="remove-workdir-form" variant="danger" :disabled="busy" data-primary>{{ busy ? 'Removing…' : 'Remove folder' }}</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.remove {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.remove__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.remove__muted {
  color: var(--omo-muted);
}
.remove__error {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
