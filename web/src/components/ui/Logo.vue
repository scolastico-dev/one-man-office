<script setup lang="ts">
// A 128 px copy of logo.png: enough for the largest use (40 px) on a 3x
// display; the full-size original is kept for the README graphics.
import logo from '../../assets/logo-128.png'

withDefaults(
  defineProps<{
    /** Edge length in px. */
    size?: number
    /** Empty when a visible name stands next to the logo. */
    alt?: string
  }>(),
  { size: 28, alt: '' },
)
</script>

<template>
  <img class="logo" :src="logo" :width="size" :height="size" :alt="alt" decoding="async" />
</template>

<style scoped>
/* White line art: shown as is on dark, darkened by the light theme's token. */
.logo {
  display: block;
  flex-shrink: 0;
  object-fit: contain;
  filter: var(--omo-logo-filter);
}
</style>
