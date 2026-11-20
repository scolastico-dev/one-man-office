<script setup lang="ts">
// The CEO's model, chosen in its tab. It is the CEO role's model list, the
// one setting Settings, Roles edits: a choice here makes the chosen model the
// list's only entry (the assignment stays). A running CEO keeps its model
// until its next start; the tab says so and offers to restart it now.
import { computed, shallowRef, useId } from 'vue'
import { getRoles, listModels, putRole } from '../../api/endpoints'
import { NO_REVISION, type AgentSession, type ApiEvent, type Model, type RoleSettings, type SettingsChangedData } from '../../api/types'
import SearchPicker from '../../components/ui/SearchPicker.vue'
import UiButton from '../../components/ui/UiButton.vue'
import IconButton from '../../components/ui/IconButton.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { visibleText } from '../../components/ui/visibleText'
import { useSession } from '../../session'
import { liveRead } from '../../store/liveRead'
import { listText, modelItems, startsWithOther, withOnlyModel } from './ceoModel'

const props = withDefaults(
  defineProps<{
    /** The running CEO, if any. */
    live?: AgentSession | null
    /** True while the CEO starts or stops: no restart is offered then. */
    busy?: boolean
    /** Keep a pending model change within the live CEO's compact toolbar. */
    compact?: boolean
  }>(),
  { live: null, busy: false, compact: false },
)
const emit = defineEmits<{ restart: [] }>()

const session = useSession()
const announcer = useAnnouncer()

const role = shallowRef<RoleSettings | null>(null)
const models = shallowRef<Model[]>([])
const error = shallowRef('')
const saving = shallowRef(false)

const list = computed(() => role.value?.models ?? [])
const text = computed(() => visibleText(listText(list.value)))
const items = computed(() => modelItems(models.value, list.value))
const next = computed(() => startsWithOther(props.live, list.value))
const nextId = useId()
const nextText = computed(() => props.live ? `The CEO runs ${visibleText(props.live.model)}; ${text.value} takes effect at its next start.` : '')

const message = (e: unknown, fallback: string) => (e instanceof Error && e.message ? e.message : fallback)

// The list changes elsewhere too (Settings, Roles; a profile that goes takes
// its models along); after a resync its revision is read again, or the next
// choice would be refused as stale.
const reader = liveRead({
  store: session.store,
  read: () => Promise.all([getRoles(session.client), listModels(session.client)]),
  apply([roles, all]) {
    role.value = roles.data.find((r) => r.name === 'ceo') ?? { name: 'ceo', models: [], assignment: 'round_robin', revision: NO_REVISION }
    models.value = all.data
    error.value = ''
  },
  failed(e) {
    error.value = message(e, 'The CEO’s model could not be read.')
  },
  watches(ev: ApiEvent) {
    const d = ev.kind === 'settings.changed' ? (ev.data as Partial<SettingsChangedData> | null) : null
    return (d?.section === 'roles' && d.name === 'ceo') || d?.section === 'profiles'
  },
})

async function choose(id: string) {
  const current = role.value
  if (current === null || saving.value) return
  if (list.value.length === 1 && list.value[0] === id) return
  saving.value = true
  error.value = ''
  try {
    const { data } = await putRole(session.client, withOnlyModel(current, id), current.revision ?? NO_REVISION)
    reader.newer()
    role.value = data
    announcer.polite(`The CEO’s model is now ${id}.`)
  } catch (e) {
    error.value = message(e, 'The CEO’s model could not be saved.')
    announcer.assertive(error.value)
    void reader.reload()
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="ceo-model" :class="{ 'ceo-model--compact': compact }" data-ceo-model>
    <SearchPicker
      :label="`Model: ${text}`"
      :button-name="`CEO model: ${text}`"
      search-label="Search models"
      placeholder="Profile or model"
      :items="items"
      empty="No model matches."
      size="sm"
      :disabled="role === null || saving"
      @select="choose"
    />
    <div v-if="next && live" class="ceo-model__next" role="status" data-ceo-model-next>
      <p :id="nextId" :class="compact ? 'sr-only' : 'ceo-model__note'">{{ nextText }}</p>
      <IconButton v-if="compact" size="sm" label="Restart now" :title="nextText" :aria-describedby="nextId" :disabled="busy" data-ceo-restart @click="emit('restart')">
        <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.3"><path d="M10.5 5a4.5 4.5 0 1 0-1 4M10.5 1.5V5H7" /></svg>
      </IconButton>
      <UiButton v-else size="sm" :disabled="busy" data-ceo-restart @click="emit('restart')">Restart now</UiButton>
    </div>
    <p v-if="error" class="ceo-model__error" role="alert">{{ error }}</p>
  </div>
</template>

<style scoped>
.ceo-model {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.ceo-model__next {
  display: flex;
  flex-basis: 100%;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.ceo-model--compact { display: grid; grid-template-columns: minmax(0, 1fr) auto; }
.ceo-model--compact .ceo-model__next { flex-basis: auto; }
.ceo-model--compact .ceo-model__error { grid-column: 1 / -1; }
.ceo-model__note,
.ceo-model__error {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.ceo-model__note {
  color: var(--omo-muted);
}
.ceo-model__error {
  flex-basis: 100%;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  color: var(--omo-fg);
}
</style>
