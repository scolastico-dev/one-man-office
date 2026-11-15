<script setup lang="ts">
// "Stop all agents": asks for confirmation, then calls `stopAllAgents`; a
// refusal shows the server's message in an error toast. For now the button
// shows its final look and does nothing.
import UiButton from '../../components/ui/UiButton.vue'
</script>

<template>
  <UiButton variant="danger" aria-label="Stop all agents" title="Stop all agents">
    <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
      <rect x="1.5" y="1.5" width="9" height="9" fill="currentColor" />
    </svg>
    <span class="topbar__label">Stop all</span>
  </UiButton>
</template>

<style scoped>
@container topbar (max-width: 767.98px) {
  .topbar__label {
    display: none;
  }
}
</style>
