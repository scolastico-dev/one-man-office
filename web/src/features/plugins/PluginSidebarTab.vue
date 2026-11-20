<script setup lang="ts">
import PluginControls from './PluginControls.vue'
import PluginTasks from './PluginTasks.vue'
import type { OwnedSidebarTab } from './frontendRuntime'
defineProps<{ tab: OwnedSidebarTab }>()
</script>

<template>
  <div class="plugin-tab" :data-plugin-tab="`${tab.plugin}:${tab.id}`">
    <PluginControls place="sidebar" :tab="tab" />
    <PluginTasks v-if="tab.tasks" :name="tab.plugin" running-only />
  </div>
</template>

<style scoped>
.plugin-tab { display: grid; align-content: start; gap: calc(var(--omo-space) * 3); padding: calc(var(--omo-space) * 3); min-width: 0; }
</style>
