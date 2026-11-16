<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    /** Keep the label as the accessible name but do not show it. */
    hideLabel?: boolean
    /**
     * Show the state in words instead of the label ("Running"/"Paused"); the
     * label stays the accessible name, the switch's checked state says the rest.
     */
    states?: { on: string; off: string }
    disabled?: boolean
    /** The control height: `sm` beside small buttons, as `UiButton`'s size. */
    size?: 'sm' | 'md'
  }>(),
  { hideLabel: false, states: undefined, disabled: false, size: 'md' },
)

const model = defineModel<boolean>({ default: false })

function toggle() {
  model.value = !model.value
}

// Space is handled on keydown; the button's own keyboard click is suppressed
// so one press toggles once. Pointer clicks and Enter go through click.
function onClick(event: MouseEvent) {
  if (event.detail === 0 && spaceDown) return
  toggle()
}
let spaceDown = false
function onKeydown(event: KeyboardEvent) {
  if (event.key !== ' ') return
  event.preventDefault()
  if (event.repeat) return
  spaceDown = true
  toggle()
}
function onKeyup(event: KeyboardEvent) {
  if (event.key !== ' ') return
  event.preventDefault()
  // The click a browser synthesises for Space follows this keyup.
  setTimeout(() => (spaceDown = false))
}
</script>

<template>
  <button
    type="button"
    role="switch"
    class="toggle"
    :class="`toggle--${size}`"
    :aria-checked="model"
    :aria-label="hideLabel || states ? label : undefined"
    :title="hideLabel ? label : undefined"
    :disabled="disabled"
    @click="onClick"
    @keydown="onKeydown"
    @keyup="onKeyup"
  >
    <span class="toggle__track" aria-hidden="true"><span class="toggle__thumb" /></span>
    <span v-if="states" class="toggle__label" :style="{ minWidth: `${Math.max(states.on.length, states.off.length)}ch` }" aria-hidden="true" data-state-word>{{ model ? states.on : states.off }}</span>
    <span v-else-if="!hideLabel" class="toggle__label">{{ label }}</span>
  </button>
</template>

<style scoped>
.toggle {
  display: inline-flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-height: var(--omo-control-h);
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.toggle--sm {
  --omo-control-h: var(--omo-control-h-sm);
}
.toggle__label {
  text-align: left;
}
.toggle:disabled {
  color: var(--omo-muted);
  cursor: not-allowed;
}
.toggle__track {
  position: relative;
  flex-shrink: 0;
  width: calc(var(--omo-space) * 8);
  height: calc(var(--omo-space) * 4.5);
  border: var(--omo-border) solid var(--omo-line-strong);
  background: var(--omo-bg);
  transition:
    background-color var(--omo-duration) ease,
    border-color var(--omo-duration) ease;
}
.toggle__thumb {
  position: absolute;
  top: calc(var(--omo-space) * 0.5);
  left: calc(var(--omo-space) * 0.5);
  width: calc(var(--omo-space) * 3);
  height: calc(var(--omo-space) * 3);
  background: var(--omo-muted);
  transition:
    transform var(--omo-duration) ease,
    background-color var(--omo-duration) ease;
}
.toggle[aria-checked='true'] .toggle__track {
  border-color: var(--omo-accent);
  background: var(--omo-accent);
}
.toggle[aria-checked='true'] .toggle__thumb {
  background: var(--omo-accent-fg);
  transform: translateX(calc(var(--omo-space) * 3.5));
}
.toggle:disabled .toggle__track {
  border-color: var(--omo-line);
}
@media (hover: hover) {
  .toggle:hover:not(:disabled) .toggle__track {
    border-color: var(--omo-accent);
  }
}
</style>
