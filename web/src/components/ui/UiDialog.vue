<script setup lang="ts">
import { inject, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { DIALOGS_IN_PLACE } from './dialogHost'
import IconButton from './IconButton.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    /** Small line above the title, e.g. the ticket number or the area. */
    eyebrow?: string
    wide?: boolean
  }>(),
  { eyebrow: undefined, wide: false },
)

const emit = defineEmits<{ close: [] }>()

/** Tests that look inside their wrapper keep dialogs where they are written. */
const inPlace = inject(DIALOGS_IN_PLACE, false)
const dialog = ref<HTMLDialogElement>()
const titleId = useId()
let opener: HTMLElement | null = null

function focusInitial() {
  const el = dialog.value
  if (!el) return
  const target =
    el.querySelector<HTMLElement>('[autofocus]') ??
    el.querySelector<HTMLElement>('.dialog__body :is(input, select, textarea):not([disabled])') ??
    el.querySelector<HTMLElement>('[data-primary]:not([disabled])') ??
    el.querySelector<HTMLElement>('.dialog__body button:not([disabled]), .dialog__actions button:not([disabled])') ??
    el.querySelector<HTMLElement>('button:not([disabled])')
  target?.focus()
}

function show() {
  const el = dialog.value
  if (!el || el.open) return
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
  el.showModal()
  void nextTick(focusInitial)
}

function hide() {
  const el = dialog.value
  if (el?.open) el.close()
  const back = opener
  opener = null
  if (back?.isConnected) back.focus()
}

watch(
  () => props.open,
  (open) => (open ? show() : hide()),
)
onMounted(() => {
  if (props.open) show()
})
onBeforeUnmount(hide)

function requestClose() {
  if (props.open) emit('close')
}

function onKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.defaultPrevented) return
  event.preventDefault()
  requestClose()
}

// The browser's own close request (Escape, or a close gesture on mobile):
// keep the dialog open and let the owner decide through `close`.
function onCancel(event: Event) {
  event.preventDefault()
  requestClose()
}
</script>

<template>
  <!-- Teleported to the body: a dialog opened from inside a form must not sit inside that form, or its own
       submit, input and click events bubble into the host (and a form inside a form is invalid HTML). -->
  <Teleport to="body" :disabled="inPlace">
    <dialog
    ref="dialog"
    class="dialog"
    :class="{ 'dialog--wide': wide }"
    :aria-labelledby="titleId"
    @keydown="onKeydown"
    @cancel="onCancel"
    @close="requestClose"
  >
    <header class="dialog__head">
      <div class="min-w-0">
        <p v-if="eyebrow" class="dialog__eyebrow">{{ eyebrow }}</p>
        <h2 :id="titleId" class="dialog__title">{{ title }}</h2>
      </div>
      <IconButton label="Close" class="dialog__close" @click="requestClose">
        <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
          <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
        </svg>
      </IconButton>
    </header>
    <div class="dialog__body"><slot /></div>
    <footer v-if="$slots.actions" class="dialog__actions"><slot name="actions" /></footer>
  </dialog>
  </Teleport>
</template>

<style scoped>
.dialog {
  width: calc(100% - var(--omo-space) * 8);
  max-width: 540px;
  max-height: calc(100dvh - var(--omo-space) * 8);
  margin: auto;
  padding: 0;
  overflow: auto;
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-surface);
  color: var(--omo-fg);
}
.dialog[open] {
  display: flex;
  flex-direction: column;
}
.dialog--wide {
  max-width: 880px;
}
.dialog::backdrop {
  background: var(--omo-backdrop);
}
.dialog__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 4);
  padding: calc(var(--omo-space) * 6) calc(var(--omo-space) * 6) 0;
}
.dialog__eyebrow {
  margin: 0 0 var(--omo-space);
  color: var(--omo-accent);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
}
.dialog__title {
  margin: 0;
  font-size: var(--omo-text-xl);
  font-weight: 700;
  line-height: var(--omo-text-xl-lh);
  overflow-wrap: anywhere;
}
.dialog__close {
  flex-shrink: 0;
  margin: calc(var(--omo-space) * -1.5) calc(var(--omo-space) * -2) 0 0;
}
.dialog__body {
  flex: 1 0 auto;
  padding: calc(var(--omo-space) * 5) calc(var(--omo-space) * 6) calc(var(--omo-space) * 6);
}
.dialog__actions {
  position: sticky;
  bottom: 0;
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: calc(var(--omo-space) * 2.5);
  padding: calc(var(--omo-space) * 4) calc(var(--omo-space) * 6);
  border-top: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
@media (max-width: 767.98px) {
  .dialog,
  .dialog--wide {
    width: 100%;
    max-width: none;
    height: 100dvh;
    max-height: none;
    border: 0;
  }
  .dialog__head {
    padding: calc(var(--omo-space) * 4) calc(var(--omo-space) * 4) 0;
  }
  .dialog__body {
    padding: calc(var(--omo-space) * 4);
  }
  .dialog__actions {
    padding: calc(var(--omo-space) * 3) calc(var(--omo-space) * 4);
  }
}
</style>
