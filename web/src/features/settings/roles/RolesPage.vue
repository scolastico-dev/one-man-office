<script setup lang="ts">
// The Roles section of the settings: the agent setup as it is, then one form
// per role to choose its models and how it picks among them. Every role saves
// on its own, so the page leaves SettingsPage's resource unset.
import { onScopeDispose, ref, shallowRef } from 'vue'
import { ApiError } from '../../../api/client'
import { listModels } from '../../../api/endpoints'
import type { ApiEvent, Model } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { SETUP_ROLES } from '../../onboarding/agentSetup'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import RoleEditor from './RoleEditor.vue'
import SetupReport from './SetupReport.vue'

const def = sectionDef('roles')
const { client } = useSession()
const store = useBoard()

const DESCRIPTIONS: Record<string, string> = {
  ceo: 'Plans across projects and talks to you in the CEO tab.',
  pm: 'Turns stories into tasks and releases them.',
  developer: 'Works on tasks: writes the code and its tests.',
  reviewer: 'Reviews finished work and gives a verdict.',
}

const models = shallowRef<Model[] | null>(null)
const failure = ref('')
let attempt = 0
let applied = 0

/** Reads the models again; an older answer never replaces a newer one. */
async function readModels() {
  const mine = ++attempt
  try {
    const { data } = await listModels(client)
    if (mine < applied) return
    applied = mine
    models.value = data
    failure.value = ''
  } catch (e) {
    if (mine < applied) return
    applied = mine
    failure.value = e instanceof ApiError ? e.message : 'The models could not be read.'
  }
}

const watches = (ev: ApiEvent) => {
  if (ev.kind === 'settings.invalid') return true
  return ev.kind === 'settings.changed' && (ev.data as { section?: string } | null)?.section === 'profiles'
}
const offEvent = store.onEvent('*', (ev) => {
  if (watches(ev)) void readModels()
})
const offSnapshot = store.onSnapshot(() => void readModels())
onScopeDispose(() => {
  offEvent()
  offSnapshot()
})
void readModels()
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <SetupReport />
    <Callout v-if="failure" tone="danger" live="assertive" data-models-error>
      <p>{{ failure }}</p>
      <template #actions><UiButton size="sm" @click="readModels()">Try again</UiButton></template>
    </Callout>
    <template v-if="models !== null">
      <RoleEditor
        v-for="name in SETUP_ROLES"
        :key="name"
        :role="name"
        :description="DESCRIPTIONS[name]"
        :models="models"
        :enabled="name !== 'ceo' || store.state.general?.ceo.enabled !== false"
      />
    </template>
    <p v-else-if="!failure" class="roles-page__note" data-models-loading>Reading the models…</p>
  </SettingsPage>
</template>

<style scoped>
.roles-page__note {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  color: var(--omo-muted);
}
</style>
