<script setup lang="ts">
import { computed } from 'vue'
import IconButton from '../../components/ui/IconButton.vue'
import Panel from '../../components/ui/Panel.vue'
import UiButton from '../../components/ui/UiButton.vue'

export interface FirstStep {
  id: string
  title: string
  detail: string
  done: boolean
  /** Label of the button that does the step. */
  action?: string
  /** Label of a second, quieter button. */
  secondary?: string
  /** Recommended, never needed: its button is never the main one. */
  optional?: boolean
  /** Its action is under way. */
  busy?: boolean
  /** The server's refusal of its action. */
  error?: string
}

const props = defineProps<{ steps: FirstStep[]; footer?: string }>()
const emit = defineEmits<{ action: [id: string]; secondary: [id: string]; dismiss: [id: string]; hide: [] }>()

/** The main button: the first step that is needed and not done. */
const mainStep = computed(() => props.steps.findIndex((s) => !s.done && !s.optional))
</script>

<template>
  <Panel label="First steps" class="steps">
    <p class="steps__intro">The board is empty. These steps get the first agent working; the ones marked recommended are up to you.</p>
    <ol class="steps__list" aria-label="First steps">
      <li v-for="(step, i) in steps" :key="step.id" class="step" :class="{ 'step--done': step.done }">
        <span class="step__mark" aria-hidden="true">
          <svg v-if="step.done" width="12" height="12" viewBox="0 0 12 12" focusable="false" fill="none" stroke="currentColor" stroke-width="1.8">
            <path d="M1.5 6.2 4.6 9.2 10.5 2.8" />
          </svg>
          <template v-else>{{ i + 1 }}</template>
        </span>
        <div class="step__body">
          <h3 class="step__title">
            {{ step.title }}<span v-if="step.done" class="step__state"> · done</span>
          </h3>
          <p class="step__detail">{{ step.detail }}</p>
          <p v-if="step.error" class="step__error" role="alert" data-step-error>{{ step.error }}</p>
        </div>
        <div v-if="step.action" class="step__actions">
          <UiButton v-if="step.secondary" size="sm" variant="ghost" data-step-secondary @click="emit('secondary', step.id)">
            {{ step.secondary }}
          </UiButton>
          <UiButton size="sm" :variant="mainStep === i ? 'primary' : 'default'" :disabled="step.busy" data-step-action @click="emit('action', step.id)">
            {{ step.busy ? `${step.action}…` : step.action }}
          </UiButton>
        </div>
        <IconButton v-if="!step.done" label="Dismiss step" size="sm" class="step__dismiss" data-step-dismiss @click="emit('dismiss', step.id)">
          <svg width="10" height="10" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
            <path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5" />
          </svg>
        </IconButton>
      </li>
    </ol>
    <div class="steps__footer">
      <p v-if="footer" class="steps__footer-text">{{ footer }}</p>
      <UiButton size="sm" variant="ghost" data-steps-hide @click="emit('hide')">Hide these steps</UiButton>
    </div>
  </Panel>
</template>

<style scoped>
.steps {
  background: var(--omo-surface);
  box-shadow: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 1.5) 0 var(--omo-backdrop);
}
.steps__intro {
  margin: 0 0 calc(var(--omo-space) * 3);
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.steps__list {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.steps__footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  margin: calc(var(--omo-space) * 3) 0 0;
}
.steps__footer-text {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.step {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto auto;
  align-items: start;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 2.5) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-raised);
}
.step__mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: calc(var(--omo-space) * 6);
  height: calc(var(--omo-space) * 6);
  border: var(--omo-border) solid var(--omo-line-strong);
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: 1;
}
.step--done .step__mark {
  border-color: var(--omo-ok);
  color: var(--omo-ok);
}
.step__title {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
}
.step__state {
  color: var(--omo-ok);
  font-weight: 400;
}
.step__detail {
  margin: var(--omo-space) 0 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.step__error {
  margin: var(--omo-space) 0 0;
  color: var(--omo-danger);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.step__dismiss {
  grid-column: 4;
  align-self: start;
}
.step__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  align-self: center;
  justify-content: flex-end;
  gap: calc(var(--omo-space) * 2);
}
@media (max-width: 479.98px) {
  .step {
    grid-template-columns: auto minmax(0, 1fr) auto;
  }
  .step__dismiss {
    grid-column: 3;
    grid-row: 1;
  }
  .step__actions {
    grid-column: 2;
    justify-self: start;
  }
}
</style>
