<script setup lang="ts">
withDefaults(
  defineProps<{
    /** Accessible name and tooltip; required because the button shows only an icon. */
    label: string
    variant?: 'default' | 'danger' | 'ghost'
    size?: 'sm' | 'md'
    disabled?: boolean
    type?: 'button' | 'submit' | 'reset'
    /** Set for on/off buttons; leave undefined for plain actions. */
    pressed?: boolean
  }>(),
  { variant: 'ghost', size: 'md', disabled: false, type: 'button', pressed: undefined },
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled"
    :aria-label="label"
    :title="label"
    :aria-pressed="pressed"
    class="omo-btn omo-btn--icon"
    :class="[`omo-btn--${variant}`, `omo-btn--${size}`]"
  >
    <slot />
  </button>
</template>
