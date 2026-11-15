<script setup lang="ts">
// Asked before the CEO is switched off. What it lists is what omo does when
// the setting changes: the tab goes, the running session ends at the next
// scheduling pass, `@ceo` is no longer offered, and what the CEO held goes to
// the user.
import UiButton from '../../../components/ui/UiButton.vue'
import UiDialog from '../../../components/ui/UiDialog.vue'

defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: []; confirm: [] }>()
</script>

<template>
  <UiDialog :open="open" title="Switch the CEO off?" eyebrow="settings" @close="emit('close')">
    <div data-ceo-switch>
      <p class="m-0">The CEO tab is hidden, a running CEO session ends at the next scheduling pass and <code>@ceo</code> stops working.</p>
      <p class="mb-0 mt-3">
        Every ticket the CEO supervised, and every open question addressed to it, is handed to you. Switching the CEO on again starts no session by itself;
        opening its tab does.
      </p>
    </div>
    <template #actions>
      <UiButton data-ceo-keep @click="emit('close')">Keep the CEO on</UiButton>
      <UiButton variant="danger" data-ceo-confirm @click="emit('confirm')">Switch the CEO off</UiButton>
    </template>
  </UiDialog>
</template>
