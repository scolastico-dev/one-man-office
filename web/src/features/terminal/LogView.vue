<script setup lang="ts">
// The readable transcript of an agent session that has ended, as plain text.
// Nothing in it is trusted: it is shown through text interpolation, with
// control and bidirectional characters written out, never as markup and never
// in the terminal emulator.
import { computed, onBeforeUnmount, shallowRef, watch } from 'vue'
import { ApiError } from '../../api/client'
import { getSessionLog } from '../../api/endpoints'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'

const props = defineProps<{ sessionId: string }>()

const session = useSession()
const announcer = useAnnouncer()

const text = shallowRef('')
const truncated = shallowRef(false)
const error = shallowRef('')
const loading = shallowRef(true)

/** Line ends and tabs stay as layout; every other invisible character is written out. */
const shown = computed(() =>
  text.value
    .split('\n')
    .map((line) => visibleText(line.replace(/\r$/, '').replace(/\t/g, '    ')))
    .join('\n'),
)

let attempt = 0
async function load(id: string): Promise<void> {
  const mine = ++attempt
  loading.value = true
  error.value = ''
  text.value = ''
  truncated.value = false
  try {
    const log = await getSessionLog(session.client, id)
    if (mine !== attempt) return
    text.value = log.text
    truncated.value = log.truncated
  } catch (e) {
    if (mine !== attempt) return
    error.value = e instanceof ApiError ? e.message : 'The log could not be read.'
    announcer.assertive(error.value)
  } finally {
    if (mine === attempt) loading.value = false
  }
}

watch(() => props.sessionId, (id) => void load(id), { immediate: true })
onBeforeUnmount(() => {
  attempt++
})
</script>

<template>
  <div class="log" data-log>
    <p v-if="loading" class="log__note" role="status">Loading the log</p>
    <p v-else-if="error" class="log__error" role="alert" data-log-error>{{ error }}</p>
    <template v-else>
      <p v-if="truncated" class="log__note" data-log-truncated>Showing the last 2 MiB.</p>
      <p v-if="text === ''" class="log__note" data-log-empty>The log is empty.</p>
      <pre v-else class="omo-logtext log__text" tabindex="0" aria-label="Log of the agent session">{{ shown }}</pre>
    </template>
  </div>
</template>

<style scoped>
.log {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.log__note,
.log__error {
  margin: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.log__note {
  color: var(--omo-muted);
}
.log__error {
  padding: var(--omo-space) calc(var(--omo-space) * 2);
  border-left: calc(var(--omo-border) * 3) solid var(--omo-warn);
  background: var(--omo-accent-soft);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.log__text {
  box-sizing: border-box;
  max-width: 100%;
  max-height: calc(var(--omo-space) * 100);
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  overflow: auto;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-term-bg);
  color: var(--omo-term-fg);
  font-family: var(--omo-font);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.log__text:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
</style>
