<script setup lang="ts">
import { computed, ref, useId, watch } from 'vue'
import { listProxyForwards, removeProxyForward } from '../../api/endpoints'
import type { ProxyForward, TicketSummary } from '../../api/types'
import Callout from '../../components/ui/Callout.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { liveRead } from '../../store/liveRead'
import TicketLink from '../ticket/TicketLink.vue'
import ForwardForm from './ForwardForm.vue'
import { openForward, openingForwards } from './openForward'

const props = defineProps<{ ticket?: TicketSummary }>()
const emit = defineEmits<{ navigate: [] }>()
const { client } = useSession()
const store = useBoard()
const heading = useId()
const formId = useId()
const rows = ref<ProxyForward[]>([])
const loading = ref(true)
const readProblem = ref('')
const actionProblem = ref('')
const createdPath = ref('')
const adding = ref(false)
const removing = ref(new Set<string>())
const canAdd = computed(() => props.ticket?.column !== 'done')
const message = (e: unknown) => visibleText(e instanceof Error ? e.message : 'Could not update forwards.')
const reads = liveRead({
  store,
  read: () => listProxyForwards(client, props.ticket?.number),
  apply: ({ data }) => {
    rows.value = data.forwards
    readProblem.value = ''
    loading.value = false
  },
  failed: (e) => {
    readProblem.value = message(e)
    loading.value = false
  },
  watches: (ev) => ev.kind === 'proxy.forward_added' || ev.kind === 'proxy.forward_updated' || ev.kind === 'proxy.forward_removed',
})
watch(() => props.ticket?.number, () => {
  reads.newer()
  rows.value = []
  loading.value = true
  adding.value = false
  createdPath.value = ''
  void reads.reload()
})
watch(canAdd, (allowed) => {
  if (!allowed) adding.value = false
})

function target(f: ProxyForward) {
  const host = f.host.includes(':') ? `[${f.host}]` : f.host
  return visibleText(`${f.scheme}://${host}:${f.port}${f.start_path}`)
}
async function added(f: ProxyForward) {
  createdPath.value = f.base_path
  adding.value = false
  await reads.reload()
}
async function remove(f: ProxyForward) {
  if (removing.value.has(f.id)) return
  removing.value.add(f.id)
  actionProblem.value = ''
  try {
    await removeProxyForward(client, f.id)
    await reads.reload()
  } catch (e) {
    actionProblem.value = message(e)
  } finally {
    removing.value.delete(f.id)
  }
}
async function open(f: ProxyForward) {
  actionProblem.value = ''
  try {
    await openForward(client, f.id)
  } catch (e) {
    actionProblem.value = message(e)
  }
}
function navigate(e: MouseEvent) {
  if (e.button === 0 && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) emit('navigate')
}
</script>

<template>
  <section class="forwards" :aria-labelledby="heading" data-forward-list>
    <div class="forwards__head">
      <h3 :id="heading" class="forwards__title">Forwards</h3>
      <UiButton v-if="canAdd" size="sm" data-forward-add :aria-expanded="adding" :aria-controls="formId" @click="adding = !adding">{{ adding ? 'Hide form' : 'Add forward' }}</UiButton>
    </div>
    <p class="forwards__hint">A forward points at an existing service on the omo host or another host. Adding or removing one never starts or stops a process. Open checks the target; an unavailable service stays listed.</p>
    <p v-if="!canAdd" class="forwards__hint">This ticket is done; new ticket forwards cannot be added.</p>
    <ForwardForm v-if="adding && canAdd" :id="formId" :ticket="ticket" @added="added" @cancel="adding = false" />
    <p v-if="createdPath" class="forwards__hint" role="status" data-forward-created>Forward added. Path-mode base path: <code>{{ visibleText(createdPath) }}</code>. Configure the app before opening it.</p>
    <p v-if="loading" role="status">Loading forwards…</p>
    <Callout v-if="readProblem" tone="danger" live="assertive">
      {{ readProblem }}
      <template #actions><UiButton size="sm" data-forward-retry @click="reads.reload()">Retry</UiButton></template>
    </Callout>
    <Callout v-if="actionProblem" tone="danger" live="assertive">{{ actionProblem }}</Callout>
    <p v-if="!loading && !readProblem && !rows.length" class="forwards__hint" data-forward-empty>No forwards{{ ticket ? ' for this ticket' : '' }}.</p>
    <ul v-if="rows.length" class="forwards__list" :aria-busy="loading">
      <li v-for="f in rows" :key="f.id" class="forwards__row" :data-forward="f.id">
        <div class="forwards__info">
          <strong>{{ visibleText(f.name || 'Unnamed forward') }}</strong>
          <span v-if="!ticket" class="forwards__owner">
            <TicketLink v-if="f.ticket !== null" :number="f.ticket" @click="navigate">Ticket #{{ f.ticket }}</TicketLink>
            <span v-else>Global</span>
          </span>
          <span class="forwards__target">{{ target(f) }}</span>
          <span class="forwards__hint">Path-mode base path: <code>{{ visibleText(f.base_path) }}</code>{{ f.strip_prefix ? ' (prefix stripped)' : ' (required by the app)' }}</span>
          <span class="forwards__hint">Address: {{ visibleText(f.address) }}</span>
        </div>
        <div class="forwards__actions">
          <UiButton size="sm" data-forward-open :aria-label="`Open ${visibleText(f.name || 'forward')} in new tab`" :disabled="openingForwards.has(f.id) || removing.has(f.id)" @click="open(f)">{{ openingForwards.has(f.id) ? 'Opening…' : 'Open' }}</UiButton>
          <UiButton size="sm" data-forward-remove :aria-label="`Remove ${visibleText(f.name || 'forward')}`" :disabled="removing.has(f.id)" @click="remove(f)">{{ removing.has(f.id) ? 'Removing…' : 'Remove' }}</UiButton>
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.forwards {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
  margin-top: calc(var(--omo-space) * 5);
}
.forwards__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
}
.forwards__title {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.forwards__hint {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.forwards__list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.forwards__row {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 3) 0;
  border-top: var(--omo-border) solid var(--omo-line);
}
.forwards__info {
  display: grid;
  flex: 1 1 220px;
  gap: var(--omo-space);
  min-width: 0;
  overflow-wrap: anywhere;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.forwards__target {
  font-family: var(--omo-font);
}
.forwards__owner {
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.forwards__actions {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
}
</style>
