<script setup lang="ts">
// Tabs after the ARIA pattern with manual activation: the arrow keys, Home
// and End move the focus along the tabs, and only Enter, Space or a click
// selects one. A tab whose selection does something (starting the CEO) is
// therefore never selected by arrowing past it.
import { computed, nextTick, ref, useId, watch } from 'vue'

export interface UiTab {
  id: string
  label: string
  /** Shown after the label in the accent colour, part of the tab's name. */
  count?: number
  /** Tooltip and description of the tab. */
  hint?: string
}

const props = withDefaults(
  defineProps<{
    tabs: UiTab[]
    /** The selected tab's id. */
    modelValue: string
    /** Accessible name of the tab list. */
    label: string
    /** Vertical tabs stand in a strip, their words turned. */
    orientation?: 'horizontal' | 'vertical'
    /** The tabs share the whole width of the list, however wide it is (else each stops at a reading width). */
    fill?: boolean
  }>(),
  { orientation: 'horizontal', fill: false },
)

const emit = defineEmits<{ 'update:modelValue': [id: string] }>()
defineSlots<{ panel?(props: { active: string }): unknown }>()

const uid = useId()
const tabId = (i: number) => `${uid}-tab-${i}`
const panelId = `${uid}-panel`

const selected = computed(() => props.tabs.findIndex((t) => t.id === props.modelValue))
/** The one tab in the tab order; follows the focus while it moves along the tabs. */
const focusIndex = ref(Math.max(0, selected.value))
watch(selected, (i) => (focusIndex.value = Math.max(0, i)))
watch(
  () => props.tabs.length,
  (n) => {
    if (focusIndex.value >= n) focusIndex.value = Math.max(0, selected.value)
  },
)

const list = ref<HTMLElement>()

function focusTab(i: number) {
  focusIndex.value = i
  void nextTick(() => list.value?.querySelectorAll<HTMLElement>('[role="tab"]')[i]?.focus())
}

function select(i: number) {
  const tab = props.tabs[i]
  if (!tab) return
  focusIndex.value = i
  if (tab.id !== props.modelValue) emit('update:modelValue', tab.id)
}

function onKeydown(e: KeyboardEvent, i: number) {
  const n = props.tabs.length
  const vertical = props.orientation === 'vertical'
  const back = vertical ? 'ArrowUp' : 'ArrowLeft'
  const forward = vertical ? 'ArrowDown' : 'ArrowRight'
  let to: number | null = null
  if (e.key === forward) to = (i + 1) % n
  else if (e.key === back) to = (i - 1 + n) % n
  else if (e.key === 'Home') to = 0
  else if (e.key === 'End') to = n - 1
  else if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    select(i)
    return
  }
  if (to === null) return
  e.preventDefault()
  focusTab(to)
}

/** Leaving the tabs puts the selected one back into the tab order. */
function onFocusout(e: FocusEvent) {
  const next = e.relatedTarget
  if (next instanceof Node && list.value?.contains(next)) return
  focusIndex.value = Math.max(0, selected.value)
}
</script>

<template>
  <div class="tabs" :class="[`tabs--${orientation}`, { 'tabs--fill': fill }]">
    <div
      ref="list"
      class="tabs__list"
      role="tablist"
      :aria-label="label"
      :aria-orientation="orientation"
      @focusout="onFocusout"
    >
      <button
        v-for="(tab, i) in tabs"
        :id="tabId(i)"
        :key="tab.id"
        type="button"
        role="tab"
        class="tabs__tab"
        :aria-selected="i === selected ? 'true' : 'false'"
        :aria-controls="$slots.panel && i === selected ? panelId : undefined"
        :aria-describedby="tab.hint ? `${tabId(i)}-hint` : undefined"
        :tabindex="i === focusIndex ? 0 : -1"
        :title="tab.hint"
        :data-tab="tab.id"
        @click="select(i)"
        @keydown="onKeydown($event, i)"
      >
        <span class="tabs__label">{{ tab.label }}</span
        ><span v-if="tab.count !== undefined" class="tabs__count">{{ ` ${tab.count}` }}</span>
        <span v-if="tab.hint" :id="`${tabId(i)}-hint`" class="sr-only">{{ tab.hint }}</span>
      </button>
    </div>
    <div
      v-if="$slots.panel && selected >= 0"
      :id="panelId"
      class="tabs__panel"
      role="tabpanel"
      :aria-labelledby="tabId(selected)"
      tabindex="0"
    >
      <slot name="panel" :active="modelValue" />
    </div>
  </div>
</template>

<style scoped>
.tabs {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}
.tabs--vertical {
  flex-direction: row;
}
.tabs__list {
  display: flex;
  flex: none;
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.tabs--vertical > .tabs__list {
  flex-direction: column;
  border-bottom: 0;
}
.tabs__tab {
  display: inline-flex;
  flex: 1 1 0;
  align-items: center;
  justify-content: center;
  min-width: 0;
  max-width: calc(var(--omo-space) * 40);
  min-height: calc(var(--omo-space) * 9);
  padding: calc(var(--omo-space) * 2);
  border: 0;
  border-right: var(--omo-border) solid var(--omo-line);
  background: transparent;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  white-space: nowrap;
  cursor: pointer;
  transition:
    background-color var(--omo-duration) ease,
    color var(--omo-duration) ease;
}
.tabs--fill .tabs__tab {
  max-width: none;
}
.tabs--vertical .tabs__tab {
  flex: none;
  max-width: none;
  min-height: 0;
  padding: calc(var(--omo-space) * 3) 0;
  border-right: 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
  writing-mode: vertical-rl;
}
.tabs__tab[aria-selected='true'] {
  background: var(--omo-select);
  box-shadow: inset 0 calc(var(--omo-border) * -2) 0 var(--omo-accent);
  color: var(--omo-fg);
}
.tabs--vertical .tabs__tab[aria-selected='true'] {
  box-shadow: inset calc(var(--omo-border) * -2) 0 0 var(--omo-accent);
}
.tabs__tab:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.tabs__label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tabs__count {
  color: var(--omo-accent);
  font-variant-numeric: tabular-nums;
  white-space: pre;
}
.tabs__panel {
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
}
.tabs__panel:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
@media (hover: hover) {
  .tabs__tab:hover {
    color: var(--omo-fg);
  }
}
</style>
