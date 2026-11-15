<script setup lang="ts">
// The usage scripts, one row each: name, whether it is omo's default, your
// version of it or one only you have, and which profiles use it. Names can
// come from files anyone with access to omo's home can write, so they are
// shown as visible text.
import { useId } from 'vue'
import type { UsageScriptInfo } from '../../../api/types'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { scriptKind, usedByWords } from './usageForms'

defineProps<{
  scripts: readonly UsageScriptInfo[]
  /** The script whose editor is open. */
  selected: string | null
  /** The id of the open editor, for aria-controls. */
  controls?: string
}>()
const emit = defineEmits<{ select: [name: string] }>()
const fallback = useId()
</script>

<template>
  <ul class="script-list" data-script-list>
    <li v-for="s in scripts" :key="s.name" class="script-list__row" :class="{ 'script-list__row--open': selected === s.name }" :data-script="s.name">
      <div class="script-list__main">
        <p class="script-list__name">
          <span>{{ visibleText(s.name) }}</span>
          <StatusBadge :tone="s.has_default && !s.customized ? 'muted' : 'accent'" :text="scriptKind(s)" data-script-kind />
        </p>
        <p class="script-list__meta" data-script-used-by>{{ visibleText(usedByWords(s.used_by)) }}</p>
      </div>
      <UiButton
        size="sm"
        :aria-expanded="selected === s.name ? 'true' : 'false'"
        :aria-controls="selected === s.name ? (controls ?? fallback) : undefined"
        data-script-toggle
        @click="emit('select', s.name)"
      >
        {{ selected === s.name ? 'Close' : 'Edit' }}<span class="sr-only"> {{ visibleText(s.name) }}</span>
      </UiButton>
    </li>
  </ul>
</template>

<style scoped>
.script-list {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.script-list__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid transparent;
}
.script-list__row--open {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
.script-list__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.script-list__row p {
  margin: 0;
  overflow-wrap: anywhere;
}
.script-list__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.script-list__meta {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.script-list__row > :deep(button) {
  flex-shrink: 0;
}
</style>
