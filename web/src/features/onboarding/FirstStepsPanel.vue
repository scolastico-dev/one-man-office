<script setup lang="ts">
import Panel from '../../components/ui/Panel.vue'
import UiButton from '../../components/ui/UiButton.vue'

export interface FirstStep {
  id: string
  title: string
  detail: string
  done: boolean
  /** Label of the button that does the step. */
  action?: string
}

defineProps<{ steps: FirstStep[] }>()
const emit = defineEmits<{ action: [id: string] }>()
</script>

<template>
  <Panel label="First steps" class="steps">
    <p class="steps__intro">The board is empty. Three steps get the first agent working.</p>
    <ol class="steps__list">
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
        </div>
        <UiButton
          v-if="step.action"
          class="step__action"
          size="sm"
          :variant="!step.done && steps.findIndex((s) => !s.done) === i ? 'primary' : 'default'"
          @click="emit('action', step.id)"
        >
          {{ step.action }}
        </UiButton>
      </li>
    </ol>
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
.step {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
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
.step__action {
  align-self: center;
}
@media (max-width: 479.98px) {
  .step {
    grid-template-columns: auto minmax(0, 1fr);
  }
  .step__action {
    grid-column: 2;
    justify-self: start;
  }
}
</style>
