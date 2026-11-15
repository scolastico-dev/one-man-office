<script setup lang="ts">
// Read-only view of the agent setup: which models each role will use and in
// which mode, which agent program and account each model belongs to, and what
// is missing. Nothing starts for a role that has no usable model.
import { computed, watch } from 'vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { DEFAULT_HOME_TEXT, PROFILE_EXAMPLE, roleExample, useAgentSetup } from './agentSetup'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const setup = useAgentSetup()
const report = computed(() => setup.report.value)

watch(
  () => props.open,
  (open) => {
    if (open) void setup.reload()
  },
)

const blockingNames = computed(() => (report.value?.blocking ?? []).map((r) => (r.name === 'ceo' ? 'CEO' : r.title.toLowerCase())))
const blockingText = computed(() => {
  const n = blockingNames.value
  const list = n.length > 1 ? `${n.slice(0, -1).join(', ')} and ${n[n.length - 1]}` : (n[0] ?? '')
  return `Nothing starts for the ${list}: ${n.length === 1 ? 'it has' : 'they have'} no usable model.`
})
const exampleModel = computed(() => report.value?.example || 'claude/sonnet')
const showProfileExample = computed(() => report.value !== null && report.value.example === '')
const lines = (m: { program: string; profile: string; account: string }) =>
  `runs ${m.program} with the profile ${m.profile}, ${m.account ? `account folder ${m.account}` : "the program's own account"}`
</script>

<template>
  <UiDialog :open="open" title="Agent setup" eyebrow="settings" wide @close="emit('close')">
    <p v-if="report === null && setup.loading.value" class="setup__note" data-loading>Reading the agent setup…</p>
    <div v-else-if="report === null" role="alert" class="setup__problem">
      <p class="m-0">{{ setup.failure.value || 'The agent setup could not be read.' }}</p>
    </div>
    <div v-else class="setup">
      <p v-if="setup.failure.value" role="alert" class="setup__problem">{{ setup.failure.value }}</p>
      <p v-if="report.blocking.length > 0" class="setup__problem" role="status" data-blocking>{{ blockingText }}</p>
      <p v-else class="setup__ok" role="status">Every role that is turned on has a usable model.</p>

      <section aria-labelledby="setup-roles">
        <h3 id="setup-roles" class="setup__heading">Roles</h3>
        <ul class="setup__list" role="list">
          <li v-for="r in report.roles" :key="r.name" class="setup__item" :data-role="r.name">
            <p class="setup__name">
              {{ r.title }}<span v-if="r.off" class="setup__muted"> (turned off, so it needs no model)</span>
            </p>
            <template v-if="r.models.length > 0">
              <p class="setup__line">Uses, in this list: {{ r.mode }}.</p>
              <ol class="setup__models">
                <li v-for="m in r.models" :key="m.id" class="setup__line" :class="{ 'setup__line--warn': m.state !== 'ok' }" :data-model="m.id">
                  {{ m.id }}
                  <template v-if="m.state === 'ok'"> {{ lines(m) }}.</template>
                  <template v-else-if="m.state === 'unknown'"> does not exist.</template>
                  <template v-else> is switched off.</template>
                </li>
              </ol>
            </template>
            <p v-for="p in r.problems" :key="p" class="setup__line setup__line--warn">{{ p }}</p>
          </li>
        </ul>
        <ul v-if="report.otherRoleErrors.length > 0" class="setup__list" role="list">
          <li v-for="e in report.otherRoleErrors" :key="e.file" class="setup__item">
            <p class="setup__line setup__line--warn">The role file {{ e.file }} cannot be used: {{ e.message }}</p>
          </li>
        </ul>
      </section>

      <section aria-labelledby="setup-profiles">
        <h3 id="setup-profiles" class="setup__heading">Profiles</h3>
        <p v-if="!report.hasProfiles" class="setup__note">No profile is defined, so there are no models.</p>
        <ul class="setup__list" role="list">
          <li v-for="p in report.profiles" :key="p.name" class="setup__item" :data-profile="p.name">
            <p class="setup__name">
              {{ p.name }} <span class="setup__muted">runs {{ p.program }}</span>
            </p>
            <p v-if="p.models.length > 0" class="setup__line">Models: {{ p.models.join(', ') }}.</p>
            <p v-else class="setup__line setup__line--warn">This profile defines no model.</p>
          </li>
          <li v-for="e in report.profileErrors" :key="e.file" class="setup__item">
            <p class="setup__line setup__line--warn">The profile file {{ e.file }} cannot be used: {{ e.message }}</p>
          </li>
        </ul>
      </section>

      <section aria-labelledby="setup-change">
        <h3 id="setup-change" class="setup__heading">How to change it</h3>
        <p class="setup__line">
          For now the setup is edited by hand; the settings pages arrive later. omo reads the folders <code>profiles</code> and <code>roles</code> in its home
          folder, which is {{ DEFAULT_HOME_TEXT }} unless omo was started with OMO_HOME set. This dialog updates as soon as a file there changes.
        </p>
        <template v-if="showProfileExample">
          <p class="setup__line">A profile names an agent program and its models. Save this as <code>profiles/claude.yaml</code>:</p>
          <pre class="setup__code" tabindex="0">{{ PROFILE_EXAMPLE }}</pre>
        </template>
        <p class="setup__line">
          A role lists the models it may use, as <code>profile/model</code>. Save this as <code>roles/developer.yaml</code> and likewise for the other roles:
        </p>
        <pre class="setup__code" tabindex="0">{{ roleExample(exampleModel) }}</pre>
        <p class="setup__line">The assignment is round_robin, random, failover or smart.</p>
      </section>
    </div>
    <template #actions>
      <UiButton v-if="setup.failure.value" @click="setup.reload()">Try again</UiButton>
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
.setup__models {
  margin: 0;
  padding-left: calc(var(--omo-space) * 5);
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
.setup__code {
  margin: calc(var(--omo-space) * 1) 0 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-raised);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-x: auto;
  white-space: pre;
}
.setup__ok {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  color: var(--omo-muted);
}
</style>
