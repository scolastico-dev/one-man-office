<script setup lang="ts">
import { computed } from 'vue'
import { useBoard } from '../../store'
import { visibleText } from '../../components/ui/visibleText'
import PluginControls from './PluginControls.vue'
import { usePluginExtensions } from './usePluginExtensions'
import { enabledPlugins } from './frontendRuntime'
const store = useBoard()
const { list, runtime } = usePluginExtensions()
const hasProjectControls = computed(() => enabledPlugins(list.data.value).some((p) => p.buttons?.some((b) => b.place === 'project')) || runtime.buttons.value.some((b) => b.place === 'project'))
const projects = computed(() => [...store.state.projects.values()].filter((p) => p.enabled && p.path))
</script>
<template>
  <div class="sidebar-plugins">
    <PluginControls place="sidebar" />
    <template v-if="hasProjectControls">
      <section v-for="p in projects" :key="p.id" :aria-label="visibleText(p.name)">
        <h3>{{ visibleText(p.name) }}</h3>
        <PluginControls place="project" :project="p.id" />
      </section>
    </template>
  </div>
</template>
<style scoped>
.sidebar-plugins:has(> :not(:empty)) { padding: calc(var(--omo-space) * 2); border-top: var(--omo-border) solid var(--omo-line); max-height: 40dvh; overflow: auto; will-change: scroll-position; }
.sidebar-plugins section { margin-top: calc(var(--omo-space) * 2); }
.sidebar-plugins h3 { margin: 0 0 var(--omo-space); font-size: var(--omo-text-sm); overflow-wrap: anywhere; }
</style>
