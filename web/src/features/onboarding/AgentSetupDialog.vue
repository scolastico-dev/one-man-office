<script setup lang="ts">
// Read-only view of the agent setup: which roles exist, which models each
// role may use, which profiles define those models, and what is missing.
// Nothing starts for a role that has no model omo knows.
import { computed, ref, watch } from 'vue'
import { ApiError } from '../../api/client'
import { getProfiles, getRoles, listModels } from '../../api/endpoints'
import type { FileError, Model, Profile, Role, RoleSettings } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { ROLE_LABELS } from '../board/model'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const session = useSession()
const store = useBoard()

const loading = ref(false)
const failure = ref('')
const roles = ref<RoleSettings[]>([])
const profiles = ref<Profile[]>([])
const profileErrors = ref<FileError[]>([])
const models = ref<Model[]>([])

let attempt = 0
async function load() {
  const my = ++attempt
  loading.value = true
  failure.value = ''
  try {
    const [r, p, m] = await Promise.all([getRoles(session.client), getProfiles(session.client), listModels(session.client)])
    if (my !== attempt) return
    roles.value = r.data
    profiles.value = p.data.profiles
    profileErrors.value = p.data.errors
    models.value = m.data
  } catch (e) {
    if (my === attempt) failure.value = e instanceof ApiError ? e.message : 'The agent setup could not be read.'
  } finally {
    if (my === attempt) loading.value = false
  }
}

watch(
  () => props.open,
  (open) => {
    if (open) void load()
    else attempt++
  },
  { immediate: true },
)

const known = computed(() => new Set(models.value.map((m) => m.id)))

/** The roles that must be able to start work: the CEO only while it is turned on. */
function needed(name: string): boolean {
  if (name === 'ceo') return store.state.general?.ceo.enabled === true
  return name === 'pm' || name === 'developer' || name === 'reviewer'
}

function label(name: string): string {
  const text = ROLE_LABELS[name as Role] ?? name
  return text.charAt(0).toUpperCase() + text.slice(1)
}

interface RoleView {
  name: string
  title: string
  usable: string[]
  unknown: string[]
  /** What is missing, in words; empty when the role is fine. */
  problem: string
  needed: boolean
}

const roleViews = computed<RoleView[]>(() =>
  roles.value.map((r) => {
    const ids = r.models ?? []
    const usable = ids.filter((id) => known.value.has(id))
    const unknown = ids.filter((id) => !known.value.has(id))
    const n = needed(r.name)
    let problem = ''
    if (ids.length === 0) problem = n ? 'No model is set, so nothing starts for this role.' : ''
    else if (usable.length === 0) problem = n ? 'None of its models is known to omo, so nothing starts for this role.' : 'None of its models is known to omo.'
    return { name: r.name, title: label(r.name), usable, unknown, problem, needed: n }
  }),
)

const blocking = computed(() => roleViews.value.filter((r) => r.needed && r.usable.length === 0))
const ceoOff = computed(() => store.state.general?.ceo.enabled === false)

function profileModels(p: Profile): string[] {
  return Object.keys(p.models ?? {}).sort()
}
</script>

<template>
  <UiDialog :open="open" title="Agent setup" eyebrow="settings" wide @close="emit('close')">
    <p v-if="loading" class="setup__note" data-loading>Reading the agent setup…</p>
    <div v-else-if="failure" role="alert" class="setup__problem">
      <p class="m-0">{{ failure }}</p>
    </div>
    <div v-else class="setup">
      <p v-if="blocking.length > 0" class="setup__problem" role="status" data-blocking>
        Nothing starts for {{ blocking.map((r) => r.title.toLowerCase()).join(', ') }}: {{ blocking.length === 1 ? 'it has' : 'they have' }} no model omo knows. Define the model in a
        profile and give it to the role.
      </p>
      <p v-else-if="roles.length > 0" class="setup__ok" role="status">Every role that is turned on has a model omo knows.</p>

      <section aria-labelledby="setup-roles">
        <h3 id="setup-roles" class="setup__heading">Roles</h3>
        <p v-if="roles.length === 0" class="setup__note">No roles are defined.</p>
        <ul v-else class="setup__list" role="list">
          <li v-for="r in roleViews" :key="r.name" class="setup__item" :data-role="r.name">
            <p class="setup__name">{{ r.title }}<span v-if="r.name === 'ceo' && ceoOff" class="setup__muted"> (turned off)</span></p>
            <p v-if="r.usable.length > 0" class="setup__line">Uses {{ r.usable.join(', ') }}.</p>
            <p v-if="r.unknown.length > 0" class="setup__line setup__line--warn">Not known to omo: {{ r.unknown.join(', ') }}.</p>
            <p v-if="r.problem" class="setup__line setup__line--warn">{{ r.problem }}</p>
          </li>
        </ul>
      </section>

      <section aria-labelledby="setup-profiles">
        <h3 id="setup-profiles" class="setup__heading">Profiles</h3>
        <p v-if="profiles.length === 0" class="setup__note">No profile is defined, so there are no models.</p>
        <ul v-else class="setup__list" role="list">
          <li v-for="p in profiles" :key="p.name" class="setup__item" :data-profile="p.name">
            <p class="setup__name">
              {{ p.name }} <span class="setup__muted">runs {{ p.command || p.cli }}</span>
            </p>
            <p v-if="profileModels(p).length > 0" class="setup__line">Models: {{ profileModels(p).join(', ') }}.</p>
            <p v-else class="setup__line setup__line--warn">This profile defines no model.</p>
          </li>
        </ul>
        <ul v-if="profileErrors.length > 0" class="setup__list" role="list">
          <li v-for="e in profileErrors" :key="e.file" class="setup__item">
            <p class="setup__line setup__line--warn">The profile file {{ e.file }} cannot be used: {{ e.message }}</p>
          </li>
        </ul>
      </section>
    </div>
    <template #actions>
      <UiButton v-if="failure" @click="load">Try again</UiButton>
      <UiButton variant="primary" @click="emit('close')">Close</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.setup {
  display: grid;
  gap: calc(var(--omo-space) * 5);
}
.setup__heading {
  margin: 0 0 calc(var(--omo-space) * 2);
  font-size: var(--omo-text-lg);
  line-height: var(--omo-text-lg-lh);
}
.setup__list {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.setup__item {
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
}
.setup__name {
  margin: 0;
  font-weight: 700;
  overflow-wrap: anywhere;
}
.setup__line,
.setup__note {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.setup__line--warn {
  color: var(--omo-danger);
}
.setup__muted {
  color: var(--omo-muted);
  font-weight: 400;
}
.setup__problem {
  margin: 0;
  padding: calc(var(--omo-space) * 2.5) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  background: var(--omo-danger-soft);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.setup__ok {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  color: var(--omo-muted);
}
</style>
