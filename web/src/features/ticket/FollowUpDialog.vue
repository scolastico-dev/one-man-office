<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { followUpTicket } from '../../api/endpoints'
import { failureText, isLostAnswer } from '../../api/lostAnswer'
import type { FollowUpRequest, TicketSummary } from '../../api/types'
import SelectField from '../../components/ui/SelectField.vue'
import TextArea from '../../components/ui/TextArea.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'

const props = defineProps<{ ticket: TicketSummary }>()
const emit = defineEmits<{ close: []; sent: [result: 'applied' | 'unknown'] }>()
const session = useSession()
const say = useAnnouncer()
const formId = useId()
const role = ref<FollowUpRequest['role']>('developer')
const comment = ref('')
const missing = ref(false)
const busy = ref(false)
const error = ref('')
const recipients = computed(() => [
  { value: 'developer', label: props.ticket.type === 'story' ? 'Product manager' : 'Developer' },
  { value: 'reviewer', label: 'Reviewer' },
])

async function submit() {
  if (busy.value) return
  const text = comment.value.trim()
  missing.value = text === ''
  if (missing.value) return
  busy.value = true
  error.value = ''
  try {
    await followUpTicket(session.client, props.ticket.number, { role: role.value, comment: text })
    emit('sent', 'applied')
  } catch (e) {
    if (isLostAnswer(e)) {
      emit('sent', 'unknown')
      return
    }
    error.value = failureText(e, {
      lost: 'No answer from omo. The panel shows whether the follow-up task was accepted.',
      refused: 'omo refused the follow-up task.',
    })
    say.assertive(error.value)
    busy.value = false
  }
}
</script>

<template>
  <UiDialog open :title="`Send follow-up task for #${ticket.number}`" :eyebrow="ticket.title" data-follow-up-dialog @close="emit('close')">
    <form :id="formId" class="follow-up" novalidate @submit.prevent="submit">
      <p class="follow-up__text">
        Choose who handles the task. After completion, the ticket's review policy sends the changes to you for review or has omo re-export them automatically. omo pushes the branch and updates the pull request.
      </p>
      <SelectField v-model="role" label="Send to" :options="recipients" :disabled="busy" />
      <TextArea
        v-model="comment"
        label="Follow-up task"
        required
        autofocus
        :rows="5"
        :disabled="busy"
        :error="missing ? 'Write the follow-up task.' : undefined"
        @input="missing = false"
      />
      <p v-if="error" class="follow-up__error" role="alert">{{ error }}</p>
    </form>
    <template #actions>
      <UiButton @click="emit('close')">Cancel</UiButton>
      <UiButton type="submit" :form="formId" variant="primary" :disabled="busy" data-primary>
        {{ busy ? 'Sending…' : 'Send task' }}
      </UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.follow-up {
  display: grid;
  gap: calc(var(--omo-space) * 4);
}
.follow-up__text,
.follow-up__error {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.follow-up__error {
  color: var(--omo-danger);
}
</style>
