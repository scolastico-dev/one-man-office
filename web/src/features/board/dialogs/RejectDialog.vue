<script setup lang="ts">
// Asks for the comment when a review is rejected by dragging it from Review to
// In Progress. Nothing is sent until the comment is confirmed.
import { ref, watch } from 'vue'
import type { TicketSummary } from '../../../api/types'
import TextArea from '../../../components/ui/TextArea.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    ticket: TicketSummary
    /** The rejection was sent and has no answer yet. */
    busy?: boolean
    /** Why omo refused the rejection. */
    error?: string
  }>(),
  { busy: false, error: '' },
)
const emit = defineEmits<{ close: []; confirm: [comment: string] }>()

const comment = ref('')
const missing = ref(false)

watch(
  () => props.open,
  (open) => {
    if (open) {
      comment.value = ''
      missing.value = false
    }
  },
)

function submit() {
  if (props.busy) return
  const text = comment.value.trim()
  missing.value = text === ''
  if (missing.value) return
  emit('confirm', text)
}
</script>

<template>
  <UiDialog :open="open" :title="`Reject #${ticket.number}?`" :eyebrow="ticket.title" @close="emit('close')">
    <form id="reject-form" class="reject" novalidate @submit.prevent="submit">
      <p class="reject__text">
        It goes back to In Progress for its developer (for a story: its product manager). Say what needs to change.
      </p>
      <TextArea
        v-model="comment"
        label="Comment"
        required
        autofocus
        :rows="5"
        :error="missing ? 'Write what needs to change.' : undefined"
        data-reject-comment
        @input="missing = false"
      />
      <p v-if="error" class="reject__error" role="alert" data-move-error>{{ error }}</p>
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton type="submit" form="reject-form" variant="danger" :disabled="busy" data-primary>Reject</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.reject {
  display: grid;
  gap: calc(var(--omo-space) * 4);
}
.reject__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.reject__error {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
