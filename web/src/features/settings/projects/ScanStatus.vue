<script setup lang="ts">
// The last scan of the workspace folders and the Rescan button. The state is
// read once, then follows `projects.scanned`; a report from Rescan replaces
// it. Answers can arrive in any order: a report older than the one shown is
// dropped, and a read started before a newer report arrived is ignored.
import { computed, onBeforeUnmount, ref } from 'vue'
import { failureText } from '../../../api/lostAnswer'
import { getScanState, rescanProjects } from '../../../api/endpoints'
import type { ScanReport } from '../../../api/types'
import RelativeTime from '../../../components/ui/RelativeTime.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { newerReport, rootCounts, rootStateText, scanSummary } from './useProjects'

const props = withDefaults(defineProps<{ /** Workspace folders stored, when known. */ roots?: number | null }>(), { roots: null })

const session = useSession()
const store = useBoard()
const announce = useAnnouncer()

const last = ref<ScanReport | null>(null)
const running = ref(false)
const loaded = ref(false)
const busy = ref(false)
const error = ref('')

/** Bumped by every report that arrives; a read started before one is old. */
let mark = 0

function take(r: ScanReport) {
  mark++
  if (newerReport(r, last.value)) last.value = r
  running.value = false
  loaded.value = true
}

async function read() {
  const at = mark
  try {
    const { data } = await getScanState(session.client)
    if (at !== mark) return
    last.value = data.last
    running.value = data.running
    error.value = ''
  } catch (e) {
    if (at !== mark) return
    error.value = failureText(e, { lost: 'The last scan could not be read.', refused: 'The last scan could not be read.' })
  } finally {
    if (at === mark) loaded.value = true
  }
}

const off = store.onEvent('projects.scanned', (ev) => {
  const r = ev.data as ScanReport | null
  if (r && typeof r === 'object' && Array.isArray(r.roots)) take(r)
})
const offSnapshot = store.onSnapshot(() => void read())
onBeforeUnmount(() => {
  off()
  offSnapshot()
})
void read()

async function rescan() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  running.value = true
  try {
    const { data } = await rescanProjects(session.client)
    take(data)
    announce.polite(`Scan finished: ${scanSummary(data)}.`)
  } catch (e) {
    running.value = false
    error.value = failureText(e, {
      lost: 'No answer from omo; the scan may still be running. The list updates when it ends.',
      refused: 'The scan could not be started.',
    })
    announce.assertive(error.value)
  } finally {
    busy.value = false
  }
}

const hasDetails = computed(() => (last.value?.roots.length ?? 0) > 0)
</script>

<template>
  <div class="scan" data-scan-status>
    <p class="scan__line" data-scan-line>
      <template v-if="running">Scanning the workspace folders…</template>
      <template v-else-if="last">Last scan <RelativeTime :at="last.finished_at" />: {{ scanSummary(last) }}</template>
      <template v-else-if="loaded && props.roots === 0">Add a workspace folder to look for repositories.</template>
      <template v-else-if="loaded">No scan yet.</template>
    </p>
    <UiButton size="sm" :disabled="busy" data-rescan @click="rescan">{{ busy ? 'Scanning…' : 'Rescan' }}</UiButton>
    <p v-if="error" class="scan__error" role="alert" data-scan-error>{{ error }}</p>
    <details v-if="last && hasDetails" class="scan__details" data-scan-details>
      <summary>Scan details</summary>
      <ul class="scan__roots">
        <li v-for="(r, i) in last.roots" :key="i" :data-scan-root="r.state">
          <p class="scan__root">
            <span class="scan__path">{{ visibleText(r.path) }}</span>: {{ rootStateText(r) }}
          </p>
          <p v-if="r.message" class="scan__note">{{ visibleText(r.message) }}</p>
          <p class="scan__note">{{ rootCounts(r) }}</p>
          <template v-if="r.skipped.length > 0">
            <p class="scan__note">Skipped, as they did not answer in time:</p>
            <ul class="scan__skipped">
              <li v-for="s in r.skipped" :key="s" data-scan-skipped>{{ visibleText(s) }}</li>
            </ul>
          </template>
        </li>
      </ul>
    </details>
  </div>
</template>

<style scoped>
.scan {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  min-width: 0;
}
.scan__line,
.scan__error,
.scan__note,
.scan__root {
  margin: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.scan__line,
.scan__note {
  color: var(--omo-muted);
}
.scan__error {
  flex-basis: 100%;
  color: var(--omo-danger);
}
.scan__details {
  flex-basis: 100%;
  min-width: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.scan__details summary {
  color: var(--omo-muted);
  cursor: pointer;
}
.scan__roots,
.scan__skipped {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: calc(var(--omo-space) * 2) 0 0;
  padding: 0;
  list-style: none;
}
.scan__skipped {
  gap: var(--omo-space);
  margin: var(--omo-space) 0 0;
  padding-left: calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  overflow-wrap: anywhere;
}
.scan__path {
  color: var(--omo-fg);
}
</style>
