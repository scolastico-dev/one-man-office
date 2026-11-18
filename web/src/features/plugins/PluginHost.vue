<script setup lang="ts">
import { onBeforeUnmount } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { disposePluginExtensions, usePluginExtensions } from './usePluginExtensions'
const { runtime, suspended } = usePluginExtensions()
const session = useSession()
onBeforeUnmount(() => disposePluginExtensions(session.store))
</script>
<template>
  <aside v-if="runtime.errors.value.length" class="plugin-errors" aria-label="Plugin frontend errors">
    <div v-for="e in runtime.errors.value" :key="e.plugin" role="alert">
      <p>{{ visibleText(e.plugin) }}: {{ e.message }}</p>
      <UiButton size="sm" :disabled="suspended" @click="runtime.retry(e.plugin)">Retry {{ visibleText(e.plugin) }}</UiButton>
    </div>
  </aside>
</template>
<style scoped>
.plugin-errors { position: fixed; z-index: 50; right: calc(var(--omo-space) * 3); top: calc(var(--omo-space) * 14); max-width: min(90vw, 48ch); max-height: 50dvh; overflow: auto; will-change: scroll-position; padding: calc(var(--omo-space) * 3); background: var(--omo-surface); border: var(--omo-border) solid var(--omo-danger); }
.plugin-errors p { overflow-wrap: anywhere; font-size: var(--omo-text-sm); }
</style>
