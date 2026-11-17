<script setup lang="ts">
// One field of the ticket: its name, its value, and either the control that
// changes it or the reason it cannot be changed. Clicking the value opens the
// control in place; Enter or Save sends the one change, Escape or Cancel
// leaves it. The server's message appears under the field.
import { nextTick, ref, useId, watch } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'

const props = withDefaults(
  defineProps<{
    field: string
    label: string
    editable: boolean
    lockedReason?: string
    editing: boolean
    saving?: boolean
    error?: string
    /** Set while the open edit's value was changed from elsewhere. */
    changedNote?: string
    /** The value is a button that opens the edit; off when the value holds links of its own. */
    clickable?: boolean
    /** Words that name the control that opens the edit, when the value is not it. */
    editLabel?: string
    /** Hides Save and Cancel for controls that save themselves. */
    plain?: boolean
  }>(),
  { lockedReason: undefined, saving: false, error: undefined, changedNote: undefined, clickable: true, editLabel: undefined, plain: false },
)
const emit = defineEmits<{ edit: []; save: []; cancel: []; 'use-new': [] }>()

const id = useId()
const labelId = `${id}-label`
const valueId = `${id}-value`
const errorId = `${id}-error`
const lockId = `${id}-lock`
const root = ref<HTMLElement>()

function focusAfter(selector: string) {
  void nextTick(() => root.value?.querySelector<HTMLElement>(selector)?.focus())
}

watch(
  () => props.editing,
  (now, before) => {
    if (now && !before) focusAfter('[data-edit-form] :is(input, select, textarea):not([disabled])')
    // Back from the control to the value, so the keyboard user is where they were.
    else if (!now && before) focusAfter('[data-edit-open]')
  },
)

// A lock can appear under an open edit (a branch was made, omo started work): nothing is sent then.
const canSave = () => props.editable && !props.saving

function onKeydown(e: KeyboardEvent) {
  // A closed list does not submit its form by itself.
  if (e.key === 'Enter' && !e.isComposing && e.target instanceof HTMLSelectElement) {
    e.preventDefault()
    if (canSave()) emit('save')
    return
  }
  if (e.key !== 'Escape' || e.defaultPrevented) return
  // Escape leaves the field, not the panel.
  e.preventDefault()
  e.stopPropagation()
  if (!props.saving) emit('cancel')
}
</script>

<template>
  <div ref="root" class="frow" :data-field="field" :data-editing="editing ? '' : undefined">
    <dt :id="labelId" class="frow__label">{{ label }}</dt>
    <dd class="frow__body">
      <template v-if="!editing">
        <button
          v-if="editable && clickable"
          type="button"
          class="frow__value"
          data-edit-open
          :aria-labelledby="`${labelId} ${valueId}`"
          :aria-describedby="error ? errorId : undefined"
          title="Click to change"
          @click="emit('edit')"
        >
          <span :id="valueId" class="frow__text"><slot name="value" /></span>
        </button>
        <div v-else class="frow__static" :class="{ 'frow__static--locked': !editable && lockedReason }">
          <div :id="valueId" class="frow__text"><slot name="value" /></div>
          <button
            v-if="editable"
            type="button"
            class="frow__change omo-btn omo-btn--sm"
            data-edit-open
            :aria-label="editLabel ?? `Change ${label.toLowerCase()}`"
            :aria-describedby="error ? errorId : undefined"
            @click="emit('edit')"
          >
            Change
          </button>
        </div>
        <p v-if="!editable && lockedReason" :id="lockId" class="frow__lock" data-locked>{{ lockedReason }}</p>
      </template>
      <form v-else class="frow__form" data-edit-form :aria-labelledby="labelId" @submit.prevent="canSave() && emit('save')" @keydown="onKeydown">
        <fieldset class="frow__fieldset" :disabled="saving || !editable">
          <slot name="edit" :label-id="labelId" />
        </fieldset>
        <p v-if="!editable && lockedReason" :id="lockId" class="frow__lock" data-locked>{{ lockedReason }}</p>
        <div v-if="changedNote" class="frow__changed" role="status" data-changed>
          <p class="frow__note">{{ changedNote }}</p>
          <UiButton size="sm" data-use-new @click="emit('use-new')">Use the new value</UiButton>
        </div>
        <div v-if="!plain" class="frow__actions">
          <UiButton type="submit" size="sm" variant="primary" :disabled="saving || !editable" data-save>{{ saving ? 'Saving…' : 'Save' }}</UiButton>
          <UiButton size="sm" :disabled="saving" data-cancel @click="emit('cancel')">Cancel</UiButton>
        </div>
      </form>
      <slot name="after" />
      <p v-if="error" :id="errorId" class="omo-field__error" role="alert" data-field-error>{{ error }}</p>
    </dd>
  </div>
</template>

<style scoped>
.frow {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.frow__label {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.frow__body {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
  margin: 0;
}
.frow__value {
  display: block;
  padding: var(--omo-space) calc(var(--omo-space) * 2);
  margin: 0 calc(var(--omo-space) * -2);
  width: calc(100% + var(--omo-space) * 4);
  border: var(--omo-border) solid transparent;
  background: none;
  color: var(--omo-fg);
  font: inherit;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-align: left;
  cursor: pointer;
}
@media (hover: hover) {
  .frow__value:hover {
    border-color: var(--omo-line-strong);
  }
}
.frow__static {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.frow__text {
  display: grid;
  gap: var(--omo-space);
  min-width: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.frow__static--locked .frow__text {
  color: var(--omo-fg);
}
.frow__lock {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.frow__form {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.frow__fieldset {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}
.frow__actions {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
.frow__changed {
  display: grid;
  justify-items: start;
  gap: var(--omo-space);
  padding: calc(var(--omo-space) * 2);
  border: var(--omo-border) solid var(--omo-warn);
  border-left-width: var(--omo-edge);
}
.frow__note {
  margin: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
@media (pointer: coarse), (max-width: 767.98px) {
  .frow__value,
  .frow__change {
    min-height: calc(var(--omo-space) * 11);
  }
}
</style>
