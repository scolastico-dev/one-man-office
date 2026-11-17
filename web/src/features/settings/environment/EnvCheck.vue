<script setup lang="ts">
// The check of the agent environment: the button (which says it runs the
// user's commands) and the answer per variable in words. Shows what the page
// gives it; the page asks the server and drops answers of edited rows.
import { computed, useId } from 'vue'
import type { EnvVarCheck } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { STATE_WORDS, summaryOf, toneOf } from './envForm'

const props = defineProps<{
  /** The answers that still match their rows. */
  checks: EnvVarCheck[]
  busy: boolean
  /** The server's refusal of the check as a whole, if it has no row. */
  error?: string
}>()
defineEmits<{ check: [] }>()

const noteId = useId()
const empty = computed(() => props.checks.length === 0)
</script>

<template>
  <div class="env-check" data-env-check>
    <div class="env-check__bar">
      <UiButton size="sm" :disabled="busy" :aria-describedby="noteId" data-env-check-run @click="$emit('check')">
        {{ busy ? 'Checking…' : 'Check (runs your commands)' }}
      </UiButton>
      <p :id="noteId" class="env-check__note">
        Check runs every command in the variables below on this machine, as you, and shows whether each variable ends up set. The results do not repeat the values.
      </p>
    </div>
    <Callout v-if="error" tone="danger" live="assertive" data-env-check-error>
      <p>{{ visibleText(error) }}</p>
    </Callout>
    <Callout v-if="!empty" :tone="toneOf(checks)" :title="summaryOf(checks)" live="polite" data-env-check-result>
      <ul class="env-check__list">
        <li v-for="c in checks" :key="c.name" :data-check="c.name" :data-state="c.state">
          <code>{{ visibleText(c.name) }}</code>: <strong>{{ STATE_WORDS[c.state] }}</strong
          ><span v-if="c.state === 'failed' && c.message">, {{ visibleText(c.message) }}</span>
        </li>
      </ul>
    </Callout>
  </div>
</template>

<style scoped>
.env-check {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.env-check__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 3);
}
.env-check__note {
  flex: 1 1 240px;
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.env-check__list {
  display: grid;
  gap: var(--omo-space);
  margin: 0;
  padding: 0;
  list-style: none;
  overflow-wrap: anywhere;
}
</style>
