<script setup lang="ts">
// "Stop all agents": asks for confirmation, then stops them (`stopAllAgents`)
// and reports the server's answer in words, including when omo refuses.
import { ref } from 'vue'
import { stopAllAgents } from '../../api/endpoints'
import { isLostAnswer } from '../../api/lostAnswer'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useToast } from '../../components/ui/useToast'
import { useSession } from '../../session'

const { client } = useSession()
const toast = useToast()
const open = ref(false)
const busy = ref(false)
const error = ref('')

function ask() {
  error.value = ''
  open.value = true
}

/** The server's answer in plain words. */
function explain(e: unknown): string {
  return e instanceof Error ? e.message : 'omo did not answer.'
}

async function confirm() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await stopAllAgents(client)
    open.value = false
    toast.show({ title: 'All agents stopped', message: 'omo is paused. The CEO is still running.' })
  } catch (e) {
    if (isLostAnswer(e)) {
      // The request may have gone through; the dialog stays so the answer is not mistaken for a failure.
      error.value = 'No answer from omo, so the agents may have been stopped. The board shows what omo did.'
      toast.show({ tone: 'warn', title: 'No answer to stopping the agents', message: 'It may or may not have happened.' })
    } else {
      const message = explain(e)
      // A toast sits under the open dialog, so the dialog shows the answer itself.
      error.value = message
      toast.show({ tone: 'error', title: 'Could not stop the agents', message })
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <UiButton variant="danger" aria-label="Stop all agents" title="Stop all agents" aria-haspopup="dialog" @click="ask">
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
      <rect x="1.5" y="1.5" width="9" height="9" fill="currentColor" />
    </svg>
    <span class="topbar__label">Stop all</span>
  </UiButton>
  <UiDialog :open="open" title="Stop every running agent?" @close="open = false">
    <p class="stop__text">
      omo pauses and ends every running agent at once, without asking them to hand over first. The CEO keeps running.
      Tickets keep their state; work in progress stays in the worktrees.
    </p>
    <p v-if="error" class="stop__error" role="alert" data-stop-error>{{ error }}</p>
    <template #actions>
      <UiButton @click="open = false">Cancel</UiButton>
      <UiButton variant="danger" :disabled="busy" data-primary @click="confirm">Stop all agents</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.stop__text {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.stop__error {
  margin: calc(var(--omo-space) * 3) 0 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
@container topbar (max-width: 767.98px) {
  .topbar__label {
    display: none;
  }
}
</style>
