<script setup lang="ts">
import { computed, useId } from 'vue'
import UiButton from './UiButton.vue'

const props = withDefaults(
  defineProps<{
    /** The form differs from what omo stored. */
    dirty: boolean
    /** A save is running. */
    busy: boolean
    /** `saved` after a save went through; `conflict` while the stored value changed under the draft. */
    status?: 'saved' | 'conflict' | null
    /** The server's refusal, verbatim. */
    error?: string
    /** Names the bar's region; a page with several forms names each one ("Save changes to the developer"). */
    label?: string
  }>(),
  { status: null, error: undefined, label: 'Save changes' },
)

const emit = defineEmits<{ save: []; discard: [] }>()

const errorId = useId()

const state = computed(() => {
  if (props.busy) return { tone: 'busy', text: 'Saving…' }
  if (props.status === 'conflict') return { tone: 'conflict', text: 'Changed outside this page' }
  if (props.dirty) return { tone: 'dirty', text: 'Unsaved changes' }
  if (props.status === 'saved') return { tone: 'saved', text: 'Saved' }
  return { tone: 'clean', text: 'No unsaved changes' }
})
</script>

<template>
  <!-- Announcing a save or a refusal is the page's job (useAnnouncer), so the bar itself is no live region. -->
  <div
    role="region"
    :aria-label="label"
    class="savebar"
    :class="[`savebar--${state.tone}`, { 'savebar--error': error }]"
    :aria-busy="busy ? 'true' : undefined"
  >
    <div class="savebar__text">
      <p class="savebar__state" data-save-state><span class="savebar__mark" aria-hidden="true" />{{ state.text }}</p>
      <p v-if="error" :id="errorId" class="savebar__error" data-save-error>{{ error }}</p>
    </div>
    <div v-if="$slots.default" class="savebar__extra"><slot /></div>
    <div class="savebar__buttons">
      <UiButton :disabled="!dirty || busy" @click="emit('discard')">Discard</UiButton>
      <UiButton variant="primary" :disabled="!dirty || busy" :aria-describedby="error ? errorId : undefined" @click="emit('save')">
        Save
      </UiButton>
    </div>
  </div>
</template>

<style scoped>
/* Sticks to the bottom of its scrolling container, so Save is always in reach. */
.savebar {
  position: sticky;
  z-index: 2;
  bottom: 0;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 4);
  min-width: 0;
  padding: calc(var(--omo-space) * 2.5) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
  transition: border-color var(--omo-duration) ease;
  --savebar-tone: var(--omo-line-strong);
}
.savebar--dirty,
.savebar--busy {
  border-color: var(--omo-accent);
  --savebar-tone: var(--omo-accent);
}
.savebar--saved {
  --savebar-tone: var(--omo-ok);
}
.savebar--conflict {
  border-color: var(--omo-warn);
  --savebar-tone: var(--omo-warn);
}
.savebar--error {
  border-color: var(--omo-danger);
}
.savebar__text {
  display: grid;
  flex: 1 1 calc(var(--omo-space) * 50);
  gap: var(--omo-space);
  min-width: 0;
}
.savebar__state {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.savebar--clean .savebar__state {
  color: var(--omo-muted);
}
.savebar--saved .savebar__state {
  color: var(--omo-ok);
}
.savebar--conflict .savebar__state {
  color: var(--omo-warn);
}
.savebar__mark {
  flex-shrink: 0;
  width: calc(var(--omo-space) * 2);
  height: calc(var(--omo-space) * 2);
  background: var(--savebar-tone);
}
.savebar__error {
  margin: 0;
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.savebar__extra,
.savebar__buttons {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.savebar__buttons {
  margin-left: auto;
}
</style>
