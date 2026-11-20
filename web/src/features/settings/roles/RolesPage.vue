<script setup lang="ts">
// The Roles section of the settings: the agent setup as it is, then one form
// per role to choose its models and how it picks among them. Each role is its
// own file with its own revision; one Save bar at the end saves every changed
// role (useSettingsGroup), and each role shows its own refusal or conflict.
import { ref, shallowRef } from 'vue'
import { ApiError } from '../../../api/client'
import { listModels } from '../../../api/endpoints'
import type { ApiEvent, Model } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import SaveBar from '../../../components/ui/SaveBar.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { liveRead } from '../../../store/liveRead'
import { roleTitle, SETUP_ROLES } from '../../onboarding/agentSetup'
import SettingsPage from '../SettingsPage.vue'
import { useSettingsGroup } from '../settingsGroup'
import { sectionDef } from '../settingsSections'
import { useLeaveGuard } from '../useLeaveGuard'
import { focusFirstInvalid } from '../useSettingsResource'
import RoleEditor from './RoleEditor.vue'
import { roleResourceOptions } from './roleResource'
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

/** Every role's file, saved together by the page's one Save bar. */
const roles = useSettingsGroup(
  SETUP_ROLES.map((role) => ({ key: role, name: `the ${role === 'ceo' ? 'CEO' : roleTitle(role).toLowerCase()}`, options: roleResourceOptions(client, role) })),
)
useLeaveGuard(roles.dirty)
const editors = ref<HTMLElement>()
async function saveAll() {
  const out = await roles.save()
  if (Object.values(out).includes('refused')) await focusFirstInvalid(editors.value)
}

const models = shallowRef<Model[] | null>(null)
const failure = ref('')

const reader = liveRead({
  store,
  read: async () => (await listModels(client)).data,
  apply(data) {
    models.value = data
    failure.value = ''
  },
  failed(e) {
    failure.value = e instanceof ApiError ? e.message : 'The models could not be read.'
  },
  watches: (ev: ApiEvent) =>
    ev.kind === 'settings.invalid' || (ev.kind === 'settings.changed' && (ev.data as { section?: string } | null)?.section === 'profiles'),
})
const readModels = () => reader.reload()
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <Callout tone="info" data-roles-note>
      <p>With round robin and random, a model listed twice is picked twice as often; with failover, smart and super smart it counts once.</p>
    </Callout>
    <SetupReport />
    <Callout v-if="failure" tone="danger" live="assertive" data-models-error>
      <p>{{ failure }}</p>
      <template #actions><UiButton size="sm" @click="readModels()">Try again</UiButton></template>
    </Callout>
    <div v-if="models !== null" ref="editors" class="roles-page__editors">
      <RoleEditor
        v-for="name in SETUP_ROLES"
        :key="name"
        :role="name"
        :resource="roles.members[name]"
        :description="DESCRIPTIONS[name]"
        :models="models"
        :enabled="name !== 'ceo' || store.state.general?.ceo.enabled !== false"
      />
    </div>
    <p v-else-if="!failure" class="roles-page__note" data-models-loading>Reading the models…</p>
    <SaveBar
      v-if="models !== null"
      :dirty="roles.dirty.value"
      :busy="roles.saving.value"
      :status="roles.status.value"
      :error="roles.error.value || undefined"
      @save="saveAll"
      @discard="roles.discard()"
    />
  </SettingsPage>
</template>

<style scoped>
.roles-page__editors {
  display: grid;
  gap: calc(var(--omo-space) * 6);
}
.roles-page__note {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  color: var(--omo-muted);
}
</style>
