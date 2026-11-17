<script setup lang="ts">
// Opens the "Create ticket" dialog. The `c` key and the add button of the
// Planning column open the same dialog through `useCreateTicket`.
import { inject, onBeforeUnmount, onMounted } from 'vue'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'
import UiButton from '../../components/ui/UiButton.vue'
import { pageKey } from '../../components/ui/shortcuts'
import { useCreateTicket } from '../create/useCreateTicket'

const create = useCreateTicket()
/** The board waits under the lock screen: its keys do nothing. */
const suspended = inject(BOARD_SUSPENDED, null)

// `c` opens the dialog (see shortcuts.ts for when it does nothing).
function onKeydown(event: KeyboardEvent) {
  if (!pageKey(event, 'c') || suspended?.value) return
  event.preventDefault()
  create.open()
}
onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))
</script>

<template>
  <UiButton variant="primary" aria-label="Create ticket" title="Create ticket (c)" @click="create.open()">
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.6">
      <path d="M6 1v10M1 6h10" />
    </svg>
    <span class="topbar__label">Create ticket</span>
  </UiButton>
</template>

<style scoped>
@container topbar (max-width: 767.98px) {
  .topbar__label {
    display: none;
  }
}
</style>
