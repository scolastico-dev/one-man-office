<script setup lang="ts">
// The stored skill sets, one row each: name, whether the checkout is there,
// who gets it and how much of it, how it updates and, once checked out, its commit and age; a failed update
// shows the server's cleaned message. Names and messages can come from files
// anyone with access to omo's home can write, so they are shown as visible text.
import { useId } from 'vue'
import type { SkillSet, SkillSetState } from '../../../api/types'
import RelativeTime from '../../../components/ui/RelativeTime.vue'
import StatusBadge from '../../../components/ui/StatusBadge.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { SKILL_SET_STATES, shortCommit, stateWords } from '../extensionWords'
import { missingState, reachWords, updateWords } from './skillForms'

const props = defineProps<{
  sets: readonly SkillSet[]
  states: readonly SkillSetState[]
  /** The set whose form is open. */
  selected: string | null
  /** The id of the open form, for aria-controls. */
  controls?: string
}>()
const emit = defineEmits<{ select: [name: string] }>()

const stateOf = (name: string): SkillSetState => props.states.find((s) => s.name === name) ?? missingState(name)
const failed = (name: string) => stateOf(name).state === 'failed'
const fallback = useId()
</script>

<template>
  <ul class="skill-list" data-skillset-list>
    <li v-for="s in sets" :key="s.name" class="skill-list__row" :class="{ 'skill-list__row--open': selected === s.name }" :data-skillset="s.name">
      <div class="skill-list__main">
        <p class="skill-list__name">
          <span>{{ visibleText(s.name) }}</span>
          <StatusBadge :tone="stateWords(SKILL_SET_STATES, stateOf(s.name).state).tone" :text="stateWords(SKILL_SET_STATES, stateOf(s.name).state).text" data-skillset-state />
          <StatusBadge v-if="!s.enabled" tone="muted" text="off" />
        </p>
        <p class="skill-list__meta" data-skillset-reach>{{ visibleText(reachWords(s)) }}</p>
        <p class="skill-list__meta">
          {{ visibleText(updateWords(s.update, s.ref)) }}
          <template v-if="stateOf(s.name).commit !== '' && stateOf(s.name).state !== 'missing'">
            · at <code>{{ shortCommit(stateOf(s.name).commit) }}</code
            ><template v-if="stateOf(s.name).updated_at">, updated <RelativeTime :at="stateOf(s.name).updated_at!" /></template>
          </template>
        </p>
        <p v-if="failed(s.name)" class="skill-list__error" data-skillset-failure>{{ visibleText(stateOf(s.name).message) }}</p>
      </div>
      <UiButton
        size="sm"
        :aria-expanded="selected === s.name ? 'true' : 'false'"
        :aria-controls="selected === s.name ? (controls ?? fallback) : undefined"
        data-skillset-toggle
        @click="emit('select', s.name)"
      >
        {{ selected === s.name ? 'Close' : 'Edit' }}<span class="sr-only"> {{ visibleText(s.name) }}</span>
      </UiButton>
    </li>
  </ul>
</template>

<style scoped>
.skill-list {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.skill-list__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border-bottom: var(--omo-border) solid var(--omo-line);
  border-left: var(--omo-edge) solid transparent;
}
.skill-list__row--open {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
.skill-list__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.skill-list__row p {
  margin: 0;
  overflow-wrap: anywhere;
}
.skill-list__name {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.skill-list__meta {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.skill-list__error {
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.skill-list__row > :deep(button) {
  flex-shrink: 0;
}
code {
  font-family: inherit;
  color: var(--omo-accent);
}
</style>
