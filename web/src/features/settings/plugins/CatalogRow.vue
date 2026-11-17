<script setup lang="ts">
// One plugin of a plugin list: its name and tag (trustTagOf: a plugin of an
// additional list is community), description, source, the list it comes
// from, and Install (or why there is none). Everything of it is data from
// the network, shown as visible text.
import { computed } from 'vue'
import type { CatalogPlugin } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { TAG_WORDS, trustTagOf, whereFrom } from './pluginTrust'

const props = defineProps<{
  plugin: CatalogPlugin
  installed: boolean
  nameTaken: boolean
  /** An install of any plugin runs; this one's when `busy` is its name. */
  busy: string | null
  error: string
}>()
defineEmits<{ install: [] }>()

const tag = computed(() => TAG_WORDS[trustTagOf(props.plugin)])
</script>

<template>
  <li class="catalog-row" :data-catalog-plugin="plugin.name" :data-tag="trustTagOf(plugin)" :data-catalog-list="plugin.list || undefined">
    <div class="catalog-row__main">
      <p class="catalog-row__name">
        <span>{{ visibleText(plugin.name) }}</span>
        <StatusBadge :tone="tag.tone" :text="tag.text" :title="tag.meaning" data-catalog-tag />
      </p>
      <p class="catalog-row__desc" data-catalog-description>{{ visibleText(plugin.description) }}</p>
      <p class="catalog-row__from" data-catalog-source>{{ visibleText(whereFrom(plugin)) }}</p>
      <p v-if="plugin.list !== ''" class="catalog-row__from" data-catalog-origin>Listed by {{ visibleText(plugin.origin) }}, not by omo</p>
      <Callout v-if="error !== ''" tone="danger" data-catalog-install-error>
        <p>{{ visibleText(error) }}</p>
      </Callout>
    </div>
    <p v-if="installed" class="catalog-row__meta" data-catalog-installed>Installed</p>
    <p v-else-if="nameTaken" class="catalog-row__meta" data-catalog-name-taken>Another plugin has this name</p>
    <UiButton
      v-else
      size="sm"
      :disabled="busy !== null"
      :aria-busy="busy === plugin.name ? 'true' : undefined"
      data-catalog-install
      @click="$emit('install')"
    >
      {{ busy === plugin.name ? 'Installing…' : 'Install' }}<span class="sr-only">{{ ' ' + visibleText(plugin.name) }}</span>
    </UiButton>
  </li>
</template>

<style scoped>
.catalog-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.catalog-row > :deep(button) {
  flex-shrink: 0;
}
.catalog-row__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.catalog-row__main p {
  margin: 0;
  overflow-wrap: anywhere;
}
.catalog-row__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.catalog-row__desc {
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.catalog-row__from {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.catalog-row__meta {
  flex-shrink: 0;
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
