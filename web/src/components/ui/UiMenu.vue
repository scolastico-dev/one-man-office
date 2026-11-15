<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'

export interface MenuItem {
  id: string
  label: string
  /** Disabled items stay reachable so their hint (the reason) can be read. */
  disabled?: boolean
  hint?: string
}

const props = withDefaults(
  defineProps<{
    /** Text of the menu button and its accessible name. */
    label: string
    items: MenuItem[]
    /** Show only the `icon` slot in the button; the label stays its name and tooltip. */
    iconOnly?: boolean
    variant?: 'default' | 'ghost'
    size?: 'sm' | 'md'
    /** Which edge of the button the menu lines up with. */
    align?: 'start' | 'end'
  }>(),
  { iconOnly: false, variant: 'default', size: 'md', align: 'start' },
)

const emit = defineEmits<{ select: [id: string] }>()

const baseId = useId()
const buttonId = `${baseId}-button`
const menuId = `${baseId}-menu`
const itemId = (i: number) => `${baseId}-item-${i}`
const hintId = (i: number) => `${baseId}-hint-${i}`

const open = ref(false)
const active = ref(0)
const root = ref<HTMLElement>()
const button = ref<HTMLButtonElement>()
const itemEls = ref<HTMLElement[]>([])

const count = computed(() => props.items.length)

function focusItem(index: number) {
  if (count.value === 0) return
  active.value = (index + count.value) % count.value
  void nextTick(() => itemEls.value[active.value]?.focus())
}

function openMenu(index: number) {
  open.value = true
  focusItem(index)
}

function closeMenu(returnFocus: boolean) {
  open.value = false
  if (returnFocus) button.value?.focus()
}

function activate(index: number) {
  const item = props.items[index]
  if (!item || item.disabled) return
  closeMenu(true)
  emit('select', item.id)
}

function onButtonKeydown(event: KeyboardEvent) {
  if (['ArrowDown', 'Enter', ' '].includes(event.key)) {
    event.preventDefault()
    openMenu(0)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    openMenu(count.value - 1)
  }
}

function onButtonClick(event: MouseEvent) {
  // Keyboard activation is handled on keydown; this is the pointer path.
  if (event.detail === 0) return
  if (open.value) closeMenu(false)
  else openMenu(0)
}

function onMenuKeydown(event: KeyboardEvent) {
  const keys: Record<string, () => void> = {
    ArrowDown: () => focusItem(active.value + 1),
    ArrowUp: () => focusItem(active.value - 1),
    Home: () => focusItem(0),
    End: () => focusItem(count.value - 1),
    Escape: () => closeMenu(true),
    Enter: () => activate(active.value),
    ' ': () => activate(active.value),
  }
  if (event.key === 'Tab') {
    closeMenu(false)
    return
  }
  const action = keys[event.key]
  if (!action) return
  event.preventDefault()
  event.stopPropagation()
  action()
}

function onPointerDownOutside(event: PointerEvent) {
  if (root.value && event.target instanceof Node && !root.value.contains(event.target)) closeMenu(false)
}

watch(open, (isOpen) => {
  if (isOpen) document.addEventListener('pointerdown', onPointerDownOutside, true)
  else document.removeEventListener('pointerdown', onPointerDownOutside, true)
})
onBeforeUnmount(() => document.removeEventListener('pointerdown', onPointerDownOutside, true))
</script>

<template>
  <div ref="root" class="menu">
    <button
      :id="buttonId"
      ref="button"
      type="button"
      class="omo-btn"
      :class="[`omo-btn--${variant}`, `omo-btn--${size}`, { 'omo-btn--icon': iconOnly }]"
      aria-haspopup="menu"
      :aria-expanded="open"
      :aria-controls="menuId"
      :aria-label="iconOnly ? label : undefined"
      :title="iconOnly ? label : undefined"
      @click="onButtonClick"
      @keydown="onButtonKeydown"
    >
      <slot name="icon" />
      <span v-if="!iconOnly">{{ label }}</span>
    </button>
    <ul
      v-show="open"
      :id="menuId"
      role="menu"
      :aria-labelledby="buttonId"
      class="menu__list"
      :class="`menu__list--${align}`"
      @keydown="onMenuKeydown"
    >
      <li
        v-for="(item, i) in items"
        :id="itemId(i)"
        :key="item.id"
        ref="itemEls"
        role="menuitem"
        tabindex="-1"
        class="menu__item"
        :aria-disabled="item.disabled ? 'true' : undefined"
        :aria-describedby="item.hint ? hintId(i) : undefined"
        @click="activate(i)"
        @pointermove="active = i"
        @focus="active = i"
      >
        <span class="menu__label">{{ item.label }}</span>
        <span v-if="item.hint" :id="hintId(i)" class="menu__hint">{{ item.hint }}</span>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.menu {
  position: relative;
  display: inline-block;
}
.menu__list {
  position: absolute;
  z-index: 30;
  top: calc(100% + var(--omo-space) * 1.5);
  min-width: 220px;
  max-width: min(420px, calc(100vw - var(--omo-space) * 8));
  margin: 0;
  padding: var(--omo-space);
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-surface);
  box-shadow: var(--omo-space) var(--omo-space) 0 var(--omo-backdrop);
  list-style: none;
}
.menu__list--start {
  left: 0;
}
.menu__list--end {
  right: 0;
}
.menu__item {
  display: grid;
  gap: 2px;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) solid transparent;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.menu__item:focus {
  border-color: var(--omo-select-line);
  background: var(--omo-select);
  outline: var(--omo-focus-width) solid var(--omo-accent);
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.menu__item[aria-disabled='true'] {
  color: var(--omo-muted);
  cursor: not-allowed;
}
.menu__hint {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
</style>
