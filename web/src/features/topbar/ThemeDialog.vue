<script setup lang="ts">
// The theme choice: follow the system, or always dark or light. The choice
// applies at once and is remembered in this browser.
import { ref, watch } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { applyTheme, readTheme, THEME_CHOICES, type ThemeChoice } from '../../theme'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

/** Browser storage can be blocked; the choice then lasts until the page is closed. */
function storage(): Storage | undefined {
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

const current = ref<ThemeChoice>('system')
watch(
  () => props.open,
  (open) => {
    if (!open) return
    const s = storage()
    const shown = document.documentElement.getAttribute('data-theme')
    current.value = s ? readTheme(s) : shown === 'dark' || shown === 'light' ? shown : 'system'
  },
  { immediate: true },
)

function choose(choice: ThemeChoice) {
  current.value = choice
  applyTheme(choice, document.documentElement, storage())
}
</script>

<template>
  <UiDialog :open="open" title="Theme" eyebrow="settings" @close="emit('close')">
    <fieldset class="theme">
      <legend class="omo-field__label">Colours</legend>
      <label v-for="c in THEME_CHOICES" :key="c.value" class="theme__option">
        <input type="radio" name="theme" :value="c.value" :checked="current === c.value" @change="choose(c.value)" />
        <span>{{ c.label }}</span>
      </label>
      <p class="omo-field__hint">System follows the colour scheme of your browser or operating system.</p>
    </fieldset>
    <template #actions>
      <UiButton variant="primary" @click="emit('close')">Close</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.theme {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  border: 0;
}
.theme__option {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 2);
  border: var(--omo-border) solid var(--omo-line);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.theme__option:has(input:checked) {
  border-color: var(--omo-accent);
  background: var(--omo-accent-soft);
}
</style>
