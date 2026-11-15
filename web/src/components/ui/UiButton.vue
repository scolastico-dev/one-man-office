<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'default' | 'primary' | 'danger' | 'ghost'
    size?: 'sm' | 'md'
    disabled?: boolean
    type?: 'button' | 'submit' | 'reset'
  }>(),
  { variant: 'default', size: 'md', disabled: false, type: 'button' },
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled"
    class="omo-btn"
    :class="[`omo-btn--${variant}`, `omo-btn--${size}`]"
    :data-primary="variant === 'primary' ? '' : undefined"
  >
    <slot />
  </button>
</template>
