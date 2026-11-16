<script setup lang="ts">
// A button that opens a search over a list of items: the search field takes
// the keyboard at once, typing narrows the list (by label and detail), arrow
// keys move through it and Enter chooses; Escape closes. Built for long lists
// (every project) where a menu would scroll for long: the list shows about a
// screenful and scrolls inside the picker. The field is a combobox with a
// listbox, so the item in reach is read out as the arrows move; an item's
// name is its label and its detail, as shown.
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { filterPicks, type PickItem } from './searchPick'

export type { PickItem } from './searchPick'

const props = withDefaults(
  defineProps<{
    /** Text of the button and its accessible name. */
    label: string
    /** Accessible name of the search field. */
    searchLabel: string
    items: PickItem[]
    /** Said when no item matches. */
    empty?: string
    placeholder?: string
    variant?: 'default' | 'ghost'
    size?: 'sm' | 'md'
    disabled?: boolean
    /** Words for how many items (not counting those always shown) match, of how many; shown under the search and read politely. */
    countText?: (matching: number, all: number) => string
  }>(),
  { empty: 'Nothing matches.', placeholder: 'Type to search', variant: 'default', size: 'md', disabled: false },
)

const emit = defineEmits<{ select: [id: string] }>()

const baseId = useId()
const popId = `${baseId}-pop`
const listId = `${baseId}-list`
const optionId = (i: number) => `${baseId}-option-${i}`
const hintId = (i: number) => `${baseId}-hint-${i}`


const open = ref(false)
const query = ref('')
const active = ref(0)
const root = ref<HTMLElement>()
const button = ref<HTMLButtonElement>()
const input = ref<HTMLInputElement>()
const list = ref<HTMLElement>()

const shown = computed(() => filterPicks(props.items, query.value))
const counted = computed(() => {
  if (!props.countText) return ''
  const all = props.items.filter((i) => !i.always).length
  return props.countText(shown.value.filter((i) => !i.always).length, all)
})
const activeId = computed(() => (shown.value.length === 0 ? undefined : optionId(active.value)))

watch(query, () => (active.value = 0))
watch(shown, (now) => {
  if (active.value >= now.length) active.value = Math.max(0, now.length - 1)
})

function openPicker() {
  if (props.disabled) return
  query.value = ''
  active.value = 0
  open.value = true
  void nextTick(() => input.value?.focus())
}

function close(returnFocus: boolean) {
  open.value = false
  if (returnFocus) button.value?.focus()
}

function choose(index: number) {
  const item = shown.value[index]
  if (!item || item.disabled) return
  close(true)
  emit('select', item.id)
}

function move(to: number) {
  const n = shown.value.length
  if (n === 0) return
  active.value = (to + n) % n
  void nextTick(() => list.value?.querySelector(`#${CSS.escape(optionId(active.value))}`)?.scrollIntoView?.({ block: 'nearest' }))
}

function onKeydown(e: KeyboardEvent) {
  const keys: Record<string, () => void> = {
    ArrowDown: () => move(active.value + 1),
    ArrowUp: () => move(active.value - 1),
    PageDown: () => move(Math.min(active.value + 8, shown.value.length - 1)),
    PageUp: () => move(Math.max(active.value - 8, 0)),
    Enter: () => choose(active.value),
    Escape: () => close(true),
  }
  if (e.key === 'Tab') {
    close(false)
    return
  }
  const action = keys[e.key]
  if (!action) return
  e.preventDefault()
  e.stopPropagation()
  action()
}

function onButtonClick() {
  if (open.value) close(false)
  else openPicker()
}

function onPointerDownOutside(e: PointerEvent) {
  if (root.value && e.target instanceof Node && !root.value.contains(e.target)) close(false)
}

watch(open, (isOpen) => {
  if (isOpen) document.addEventListener('pointerdown', onPointerDownOutside, true)
  else document.removeEventListener('pointerdown', onPointerDownOutside, true)
})
onBeforeUnmount(() => document.removeEventListener('pointerdown', onPointerDownOutside, true))
</script>

<template>
  <div ref="root" class="pick">
    <button
      ref="button"
      type="button"
      class="omo-btn"
      :class="[`omo-btn--${variant}`, `omo-btn--${size}`]"
      aria-haspopup="dialog"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-controls="popId"
      :disabled="disabled"
      @click="onButtonClick"
    >
      {{ label }}
    </button>
    <div v-if="open" :id="popId" class="pick__pop" data-pick>
      <input
        ref="input"
        v-model="query"
        type="text"
        class="omo-input pick__search"
        role="combobox"
        :aria-label="searchLabel"
        aria-expanded="true"
        aria-autocomplete="list"
        :aria-controls="listId"
        :aria-activedescendant="activeId"
        :placeholder="placeholder"
        autocomplete="off"
        spellcheck="false"
        @keydown="onKeydown"
      />
      <p v-if="counted" class="pick__count" role="status" data-pick-count>{{ counted }}</p>
      <ul :id="listId" ref="list" role="listbox" :aria-label="label" class="pick__list">
        <li
          v-for="(item, i) in shown"
          :id="optionId(i)"
          :key="item.id"
          role="option"
          class="pick__item"
          :class="{ 'pick__item--active': i === active }"
          :aria-selected="i === active ? 'true' : 'false'"
          :aria-disabled="item.disabled ? 'true' : undefined"
          :aria-describedby="item.hint ? hintId(i) : undefined"
          :data-pick-item="item.id"
          @click="choose(i)"
          @pointermove="active = i"
        >
          <span class="pick__label" data-pick-label>{{ item.label }}</span>
          <span v-if="item.detail" class="pick__detail" :title="item.detail">{{ item.detailShown ?? item.detail }}</span>
          <span v-if="item.hint" :id="hintId(i)" class="pick__hint">{{ item.hint }}</span>
        </li>
      </ul>
      <p v-if="shown.length === 0" class="pick__empty" data-pick-empty>{{ empty }}</p>
    </div>
  </div>
</template>

<style scoped>
.pick {
  position: relative;
  display: inline-block;
}
.pick__pop {
  position: absolute;
  z-index: 30;
  top: calc(100% + var(--omo-space) * 1.5);
  left: 0;
  display: grid;
  gap: var(--omo-space);
  width: min(400px, calc(100vw - var(--omo-space) * 14));
  padding: var(--omo-space);
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-surface);
  box-shadow: var(--omo-space) var(--omo-space) 0 var(--omo-backdrop);
}
.pick__search {
  width: 100%;
}
/* About a screenful; the rest scrolls inside the picker. */
.pick__list {
  max-height: min(55vh, calc(var(--omo-space) * 100));
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;
}
.pick__item {
  display: grid;
  gap: 2px;
  min-width: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) solid transparent;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.pick__item--active {
  border-color: var(--omo-select-line);
  background: var(--omo-select);
  outline: var(--omo-focus-width) solid var(--omo-accent);
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.pick__item[aria-disabled='true'] {
  color: var(--omo-muted);
  cursor: not-allowed;
}
.pick__label {
  overflow-wrap: anywhere;
}
.pick__count {
  margin: 0;
  padding: 0 calc(var(--omo-space) * 2.5);
}
.pick__count,
.pick__detail,
.pick__hint,
.pick__empty {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.pick__detail {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pick__hint {
  overflow-wrap: anywhere;
}
.pick__empty {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
}
</style>
