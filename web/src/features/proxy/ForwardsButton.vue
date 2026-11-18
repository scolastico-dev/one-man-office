<script setup lang="ts">
import { ref } from 'vue'
import IconButton from '../../components/ui/IconButton.vue'
import { ChunkFailedDialog, lazyComponent } from '../../components/ui/chunkFailure'
const ForwardsDialog = lazyComponent(() => import('./ForwardsDialog.vue'), { errorComponent: ChunkFailedDialog })
const open = ref(false)
</script>
<template>
  <IconButton label="Forwards" aria-haspopup="dialog" data-forwards-button @click="open = true">
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
      <path d="M8 2h4v4M12 2 6 8M6 3H2v9h9V8" />
    </svg>
  </IconButton>
  <ForwardsDialog v-if="open" @close="open = false" />
</template>
