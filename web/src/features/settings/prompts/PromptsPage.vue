<script setup lang="ts">
// The Prompts section of the settings: what agents are told. Each role has
// your instructions (inserted into its prompt) and omo's prompt for the role;
// then the short messages omo types into a running agent. One prompt is
// edited at a time, each editor with its own resource; opening another one
// while the open one has unsaved changes asks first.
import { computed, nextTick, ref } from 'vue'
import { listPrompts } from '../../../api/endpoints'
import type { PromptInfo, SettingsChangedData } from '../../../api/types'
import Callout from '../../../components/ui/Callout.vue'
import FormSection from '../../../components/ui/FormSection.vue'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useSession } from '../../../session'
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import { askFirst, LEAVE_QUESTION } from '../useLeaveGuard'
import { useSettingsResource } from '../useSettingsResource'
import PromptEditor from './PromptEditor.vue'
import { groupPrompts, type PromptEntry } from './promptGroups'

const def = sectionDef('prompts')
const { client } = useSession()

const list = useSettingsResource<{ prompts: PromptInfo[] }>({
  load: async () => {
    const { data } = await listPrompts(client)
    return { value: { prompts: data }, revision: '0' }
  },
  save: () => Promise.reject(new Error('The list of prompts is read only.')),
  watches: (ev) => ev.kind === 'settings.invalid' || (ev.kind === 'settings.changed' && (ev.data as Partial<SettingsChangedData> | null)?.section === 'prompts'),
})

const groups = computed(() => groupPrompts(list.value.value?.prompts ?? []))
const loadFailed = computed(() => list.value.value === null && !list.loading.value && list.error.value !== '')

/** The prompt whose editor is open. */
const open = ref<string | null>(null)
const dirty = ref(false)

async function toggle(entry: PromptEntry) {
  const next = open.value === entry.name ? null : entry.name
  if (dirty.value && !(await askFirst(LEAVE_QUESTION))) return
  dirty.value = false
  open.value = next
  if (next !== null) await nextTick()
}

const status = (e: PromptEntry) => (e.kind === 'instructions' ? (e.customized ? 'Set' : 'Empty') : e.customized ? 'Customized' : "omo's default")
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <Callout tone="info">
      <p>Agents read these prompts and run as you, so anything written here is no secret from them. Only you change prompts.</p>
    </Callout>
    <p v-if="list.loading.value && !list.value.value" class="prompts__wait" data-settings-loading>Reading the prompts…</p>
    <Callout v-else-if="loadFailed" tone="danger" live="assertive" data-settings-load-error>
      <p>{{ list.error.value }}</p>
      <template #actions><UiButton size="sm" @click="list.reload()">Try again</UiButton></template>
    </Callout>

    <FormSection v-for="g in groups" :key="g.id" :legend="g.title" :description="g.description" :data-prompt-group="g.id">
      <div v-for="p in g.prompts" :key="p.name" class="prompts__row" :data-prompt-row="p.name">
        <div class="prompts__head">
          <div class="prompts__what">
            <h3 class="prompts__title">{{ visibleText(p.title) }}</h3>
            <p class="prompts__desc">{{ p.description }}</p>
          </div>
          <div class="prompts__side">
            <span class="prompts__state" data-prompt-state>{{ status(p) }}</span>
            <UiButton
              size="sm"
              :variant="p.kind === 'role' ? 'ghost' : 'default'"
              :aria-expanded="open === p.name"
              :aria-label="p.kind === 'message' ? `${open === p.name ? 'Close' : 'Edit'} ${visibleText(p.title)}` : undefined"
              data-prompt-toggle
              @click="toggle(p)"
            >
              {{ open === p.name ? 'Close' : p.action }}
            </UiButton>
          </div>
        </div>
        <PromptEditor v-if="open === p.name" :key="p.name" :name="p.name" :title="p.kind === 'message' ? visibleText(p.title) : p.title" focus @dirty="dirty = $event" />
      </div>
    </FormSection>
  </SettingsPage>
</template>

<style scoped>
.prompts__wait {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.prompts__row {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.prompts__row + .prompts__row {
  padding-top: calc(var(--omo-space) * 3);
  border-top: var(--omo-border) solid var(--omo-line);
}
.prompts__head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 4);
  min-width: 0;
}
.prompts__what {
  min-width: 0;
  flex: 1 1 calc(var(--omo-space) * 60);
}
.prompts__title {
  margin: 0;
  font-size: var(--omo-text-base);
  line-height: var(--omo-text-base-lh);
}
.prompts__desc {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.prompts__side {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 3);
}
.prompts__state {
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
