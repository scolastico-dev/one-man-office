<script setup lang="ts">
// Detect agent programs: says before the click what detection does, asks the
// server to do it, and shows exactly what it answered: the programs found,
// the profiles written, the roles that got models and what was skipped (the
// server's sentences). Paths and names come from this computer and the
// settings files, so they are shown as visible text.
import { computed, ref } from 'vue'
import { detectAgents } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { DetectResult } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'

const emit = defineEmits<{ detected: [DetectResult] }>()

const session = useSession()
const announce = useAnnouncer()
const busy = ref(false)
const result = ref<DetectResult | null>(null)
const error = ref('')

const summary = computed(() => {
  const r = result.value
  if (r === null) return ''
  const found = r.found.length === 0 ? 'No agent program found.' : `Found ${r.found.length === 1 ? 'one agent program' : `${r.found.length} agent programs`}.`
  const created = r.created.length === 0 ? 'No profile written.' : `Profiles written: ${r.created.map(visibleText).join(', ')}.`
  return `${found} ${created}`
})

async function detect() {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    const { data } = await detectAgents(session.client)
    result.value = data
    announce.polite(summary.value)
    emit('detected', data)
  } catch (e) {
    error.value = failureText(e, {
      lost: 'No answer from omo, so detection may have written profiles. Look in the profile list.',
      refused: 'Detection failed.',
    })
    announce.assertive(error.value)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="detect" data-detect>
    <p class="detect__text" data-detect-explained>
      Looks for claude, codex, agy and gemini on this computer's PATH; it does not run them. For each program found that has no profile of its
      kind yet, it writes a profile with the program's models and gives them to the roles that have no model. These profiles start
      agents with an argument that skips the program's permission prompts, so agents act without asking; the arguments are listed in
      each profile and can be changed there.
    </p>
    <div>
      <UiButton :disabled="busy" data-detect-button @click="detect">{{ busy ? 'Detecting…' : 'Detect agent programs' }}</UiButton>
    </div>
    <Callout v-if="error" tone="danger" live="assertive" data-detect-error>
      <p>{{ error }}</p>
    </Callout>
    <Callout v-if="result" tone="info" title="Detection" live="polite" data-detect-result>
      <p v-if="result.found.length === 0" data-detect-found-none>No agent program found on the PATH.</p>
      <template v-else>
        <p>Found:</p>
        <ul class="detect__list">
          <li v-for="f in result.found" :key="f.cli + f.path" data-detect-found>{{ visibleText(f.cli) }}: {{ visibleText(f.path) }}</li>
        </ul>
      </template>
      <p v-if="result.created.length === 0" data-detect-created-none>No profile written.</p>
      <p v-else data-detect-created>Profiles written: {{ result.created.map(visibleText).join(', ') }}.</p>
      <p v-if="result.assigned.length" data-detect-assigned>Roles that got models: {{ result.assigned.map(visibleText).join(', ') }}.</p>
      <template v-if="result.skipped.length">
        <p>Skipped:</p>
        <ul class="detect__list">
          <li v-for="s in result.skipped" :key="s" data-detect-skipped>{{ visibleText(s) }}</li>
        </ul>
      </template>
    </Callout>
  </div>
</template>

<style scoped>
.detect {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.detect__text {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.detect__list {
  margin: 0;
  padding-left: calc(var(--omo-space) * 5);
  overflow-wrap: anywhere;
}
</style>
