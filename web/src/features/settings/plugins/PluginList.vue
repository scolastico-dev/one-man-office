<script setup lang="ts">
// The installed plugins, one row each: name, version, state in words, what
// it does and when it last ran. A plugin whose settings file exists without
// its files shows as not installed. Names, versions and descriptions come
// from files below omo's home, so they are shown as visible text.
import { computed, useId } from 'vue'
import type { PluginInfo, PluginSettings } from '../../../api/types'
import RelativeTime from '../../../components/ui/RelativeTime.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { eventNoun, stateWords } from './pluginForms'

const props = defineProps<{
  plugins: readonly PluginInfo[]
  settings: readonly PluginSettings[]
  /** The plugin whose box is open. */
  selected: string | null
  /** The id of the open box, for aria-controls. */
  controls?: string
}>()
const emit = defineEmits<{ select: [name: string] }>()
const fallback = useId()

/** Every plugin the runtime knows, then settings files it does not know (not installed), by name. */
const rows = computed<PluginInfo[]>(() => {
  const out = [...props.plugins]
  for (const s of props.settings) {
    if (!out.some((p) => p.name === s.name)) out.push({ name: s.name, version: '', description: '', state: 'not_installed', message: '', commit: '', last_event: '', last_run_at: null, hooks: [] })
  }
  return out.sort((a, b) => a.name.localeCompare(b.name))
})
</script>

<template>
  <ul class="plugin-list" data-plugin-list>
    <li v-for="p in rows" :key="p.name" class="plugin-list__row" :class="{ 'plugin-list__row--open': selected === p.name }" :data-plugin="p.name">
      <div class="plugin-list__main">
        <p class="plugin-list__name">
          <span>{{ visibleText(p.name) }}</span>
          <span v-if="p.version" class="plugin-list__version">{{ visibleText(p.version) }}</span>
          <StatusBadge :tone="stateWords(p.state).tone" :text="stateWords(p.state).text" data-plugin-state />
        </p>
        <p v-if="p.description" class="plugin-list__desc">{{ visibleText(p.description) }}</p>
        <p class="plugin-list__meta" data-plugin-last-run>
          <template v-if="p.last_run_at">Last ran <RelativeTime :at="p.last_run_at" />, on {{ visibleText(eventNoun(p.last_event)) }}</template>
          <template v-else>Has not run yet</template>
        </p>
      </div>
      <UiButton
        size="sm"
        :aria-expanded="selected === p.name ? 'true' : 'false'"
        :aria-controls="selected === p.name ? (controls ?? fallback) : undefined"
        data-plugin-toggle
        @click="emit('select', p.name)"
      >
        {{ selected === p.name ? 'Close' : 'Open' }}<span class="sr-only"> {{ visibleText(p.name) }}</span>
      </UiButton>
    </li>
  </ul>
</template>

<style scoped>
.plugin-list {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.plugin-list__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid transparent;
}
.plugin-list__row--open {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
.plugin-list__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.plugin-list__row p {
  margin: 0;
  overflow-wrap: anywhere;
}
.plugin-list__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.plugin-list__version,
.plugin-list__meta {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.plugin-list__desc {
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.plugin-list__row > :deep(button) {
  flex-shrink: 0;
}
</style>
