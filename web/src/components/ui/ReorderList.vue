<script setup lang="ts">
import { nextTick, ref } from 'vue'
import IconButton from './IconButton.vue'
import { useAnnouncer } from './useAnnouncer'

export interface ReorderItem {
  /** Need not be unique: a model listed twice appears twice. */
  id: string
  label: string
  /** A short line under the label, e.g. the profile or a warning in words. */
  note?: string
}

const props = withDefaults(
  defineProps<{
    items: ReorderItem[]
    /** Names the list, e.g. "Models of the developer". */
    label: string
    /** Shown instead of the list when it is empty. */
    emptyText?: string
    /** Offer Remove on every row. */
    removable?: boolean
  }>(),
  { emptyText: undefined, removable: true },
)

/** The owner changes `items`; the list only asks. */
const emit = defineEmits<{ move: [from: number, to: number]; remove: [index: number] }>()
defineSlots<{ item?(props: { item: ReorderItem; index: number }): unknown }>()

const announcer = useAnnouncer()
const root = ref<HTMLElement>()

function focus(i: number, part: string): boolean {
  const el = root.value?.querySelector<HTMLButtonElement>(`[data-row="${i}"] [data-part="${part}"]`)
  if (!el || el.disabled) return false
  el.focus()
  return true
}

function move(from: number, to: number) {
  const item = props.items[from]
  if (!item || to < 0 || to >= props.items.length) return
  emit('move', from, to)
  announcer.polite(`${item.label} moved to place ${to + 1}.`)
  const same = to < from ? 'up' : 'down'
  const other = to < from ? 'down' : 'up'
  void nextTick(() => focus(to, same) || focus(to, other))
}

function remove(index: number) {
  const item = props.items[index]
  if (!item) return
  emit('remove', index)
  announcer.polite(`${item.label} removed.`)
  void nextTick(() => focus(index, 'remove') || focus(index - 1, 'remove') || root.value?.focus())
}
</script>

<template>
  <div ref="root" class="reorder" tabindex="-1">
    <ol v-if="items.length" class="reorder__list" :aria-label="label">
      <li v-for="(item, i) in items" :key="`${i}:${item.id}`" class="reorder__row" :data-row="i">
        <span class="reorder__place" aria-hidden="true">{{ i + 1 }}</span>
        <div class="reorder__main">
          <slot name="item" :item="item" :index="i">
            <span class="reorder__label">{{ item.label }}</span>
            <span v-if="item.note" class="reorder__note">{{ item.note }}</span>
          </slot>
        </div>
        <div class="reorder__buttons">
          <IconButton :label="`Move ${item.label} up from place ${i + 1}`" :disabled="i === 0" data-part="up" @click="move(i, i - 1)">
            <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M2 8l4-4 4 4" />
            </svg>
          </IconButton>
          <IconButton
            :label="`Move ${item.label} down from place ${i + 1}`"
            :disabled="i === items.length - 1"
            data-part="down"
            @click="move(i, i + 1)"
          >
            <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M2 4l4 4 4-4" />
            </svg>
          </IconButton>
          <IconButton v-if="removable" :label="`Remove ${item.label} from place ${i + 1}`" data-part="remove" @click="remove(i)">
            <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
              <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
            </svg>
          </IconButton>
        </div>
      </li>
    </ol>
    <p v-else-if="emptyText" class="reorder__empty">{{ emptyText }}</p>
  </div>
</template>

<style scoped>
.reorder {
  min-width: 0;
}
.reorder:focus {
  outline: none;
}
.reorder__list {
  margin: 0;
  padding: 0;
  border-top: var(--omo-border) solid var(--omo-line);
  list-style: none;
}
.reorder__row {
  display: grid;
  grid-template-columns: calc(var(--omo-space) * 6) minmax(0, 1fr) auto;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-height: calc(var(--omo-space) * 11);
  padding: calc(var(--omo-space) * 1.5) 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.reorder__place {
  color: var(--omo-accent);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-align: right;
}
.reorder__main {
  display: grid;
  min-width: 0;
}
.reorder__label {
  overflow: hidden;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.reorder__note {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.reorder__buttons {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 0.5);
}
.reorder__buttons > .omo-btn:is(:disabled, [aria-disabled='true']) {
  border-color: transparent;
  color: var(--omo-line-strong);
}
.reorder__empty {
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) dashed var(--omo-line);
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
