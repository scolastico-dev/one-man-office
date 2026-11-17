<script setup lang="ts">
import { computed } from 'vue'
import IconButton from './IconButton.vue'
import UiButton from './UiButton.vue'
import { topModal } from './modalLayer'
import { activeToasts, pauseToast, resumeToast, useToast, type Toast } from './useToast'

const { dismiss } = useToast()
const errors = computed(() => activeToasts.filter((t) => t.tone === 'error'))
const others = computed(() => activeToasts.filter((t) => t.tone !== 'error'))
const groups = computed(() => [
  { role: 'status', toasts: others.value },
  { role: 'alert', toasts: errors.value },
])

function runAction(toast: Toast) {
  toast.action?.run()
  dismiss(toast.id)
}
</script>

<template>
  <!-- An open modal dialog makes everything outside it inert: the toasts then live inside the topmost one, so
       their actions can still be reached by keyboard and pointer. -->
  <Teleport :to="topModal ?? 'body'" :disabled="!topModal">
  <div class="toaster">
    <!-- Both live regions always exist, so screen readers announce what is added to them. -->
    <div v-for="group in groups" :key="group.role" :role="group.role" class="toaster__region">
      <div
        v-for="toast in group.toasts"
        :key="toast.id"
        class="toast"
        :class="`toast--${toast.tone}`"
        :data-toast="toast.id"
        @pointerenter="pauseToast(toast.id)"
        @pointerleave="resumeToast(toast.id)"
        @focusin="pauseToast(toast.id)"
        @focusout="resumeToast(toast.id)"
      >
        <div class="toast__text">
          <p class="toast__title">{{ toast.title }}</p>
          <p v-if="toast.message" class="toast__message">{{ toast.message }}</p>
          <UiButton v-if="toast.action" size="sm" class="toast__action" @click="runAction(toast)">
            {{ toast.action.label }}
          </UiButton>
        </div>
        <IconButton label="Dismiss" size="sm" @click="dismiss(toast.id)">
          <svg width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
            <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </IconButton>
      </div>
    </div>
  </div>
  </Teleport>
</template>

<style scoped>
.toaster {
  position: fixed;
  z-index: 50;
  right: calc(var(--omo-space) * 4);
  bottom: calc(var(--omo-space) * 4);
  display: flex;
  flex-direction: column;
  gap: calc(var(--omo-space) * 2);
  width: min(400px, calc(100vw - var(--omo-space) * 8));
  pointer-events: none;
}
.toaster__region {
  display: contents;
}
.toast {
  display: flex;
  align-items: flex-start;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 3) calc(var(--omo-space) * 2) calc(var(--omo-space) * 3) calc(var(--omo-space) * 3.5);
  border: var(--omo-border) solid var(--omo-line-strong);
  border-left: var(--omo-edge) solid var(--omo-accent);
  background: var(--omo-surface);
  box-shadow: var(--omo-space) var(--omo-space) 0 var(--omo-backdrop);
  pointer-events: auto;
}
.toast--warn {
  border-left-color: var(--omo-warn);
}
.toast--error {
  border-color: var(--omo-danger);
  border-left-color: var(--omo-danger);
}
.toast__text {
  display: grid;
  flex: 1;
  gap: var(--omo-space);
  min-width: 0;
}
.toast__title {
  margin: 0;
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.toast--error .toast__title {
  color: var(--omo-danger);
}
.toast--warn .toast__title {
  color: var(--omo-warn);
}
.toast__message {
  margin: 0;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
.toast__action {
  justify-self: start;
  margin-top: var(--omo-space);
}
@media (max-width: 767.98px) {
  .toaster {
    right: calc(var(--omo-space) * 2);
    bottom: calc(var(--omo-space) * 2);
    width: calc(100vw - var(--omo-space) * 4);
  }
}
</style>
