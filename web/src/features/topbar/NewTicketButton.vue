<script setup lang="ts">
// Opens the "Create ticket" dialog. The `c` key and the add button of the
// Planning column open the same dialog through `useCreateTicket`.
import { onBeforeUnmount, onMounted } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useCreateTicket } from '../create/useCreateTicket'

const create = useCreateTicket()

/** Whether a key press belongs to something the user is typing in. */
function typing(target: EventTarget | null): boolean {
  return target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
}

// `c` opens the dialog, unless it is typed into a field or a dialog is open.
function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'c' || event.ctrlKey || event.metaKey || event.altKey || event.shiftKey) return
  if (event.defaultPrevented || event.isComposing || typing(event.target)) return
  if (document.querySelector('dialog[open]')) return
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
