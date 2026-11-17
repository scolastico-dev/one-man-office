<script setup lang="ts">
// The head of one project's diff, in the panel's Diff tab and in the file
// browser of the ticket's page: what is compared with what, the notice and
// switch for a base branch behind its remote-tracking branch, and the
// project's state or message. A done ticket's diff runs between the commits
// recorded then, which the head names. Branch names and git's messages come
// from the agent's work and are shown through visibleText.
import { computed } from 'vue'
import type { ProjectDiff, TicketSummary } from '../../api/types'
import Toggle from '../../components/ui/Toggle.vue'
import { visibleText } from '../../components/ui/visibleText'
import { shortCommit } from './shortCommit'
import { behindWords, remoteWords, type DiffController } from './useDiff'

const props = defineProps<{
  d: DiffController
  project: ProjectDiff
  ticket: TicketSummary
  /** The project's name as shown. */
  name: string
}>()

/** The remote-tracking branch, when the switch to it is offered: its base is behind, or it is compared with it already. */
const remoteBase = computed(() => {
  const p = props.project
  const rb = p.remote_base
  if (!rb || p.state !== 'ok') return null
  const remote = p.compared_with === 'remote'
  return rb.behind > 0 || remote ? { rb, remote } : null
})
</script>

<template>
  <header class="dhead">
    <h4 class="dhead__name">{{ name }}</h4>
    <p v-if="project.state === 'ok' && project.recorded" class="dhead__sum" data-diff-heading data-diff-recorded>
      Changes as recorded when the ticket was done:
      <code :title="project.base_commit">{{ shortCommit(project.base_commit) }}</code>..<code :title="project.head_commit">{{
        shortCommit(project.head_commit)
      }}</code>
    </p>
    <p v-else-if="project.state === 'ok'" class="dhead__sum" data-diff-heading>
      Changes on <code>{{ visibleText(project.branch) }}</code> since it left
      <template v-if="ticket.story !== null">the story's branch </template
      ><template v-else-if="project.compared_with === 'remote'">the remote-tracking branch </template><code>{{ visibleText(project.base) }}</code>
    </p>
  </header>
  <div v-if="remoteBase" class="dhead__remote" data-diff-remote>
    <p v-if="remoteBase.remote" class="dhead__line" data-diff-compared>{{ visibleText(remoteWords(remoteBase.rb)) }}</p>
    <p v-else class="dhead__line" data-diff-behind>{{ visibleText(behindWords(remoteBase.rb)) }}</p>
    <Toggle
      :model-value="remoteBase.remote"
      :label="`Compare with ${visibleText(remoteBase.rb.name)}`"
      :disabled="d.refreshing.value"
      data-diff-remote-switch
      @update:model-value="(on: boolean) => d.setRemote(project.project, on)"
    />
  </div>
  <p v-if="d.baseError.value" class="dhead__note dhead__note--error" role="alert" data-diff-base-error>
    {{ visibleText(d.baseError.value) }} The diff compares with the local base branch.
  </p>
  <p v-if="project.state !== 'ok'" class="dhead__note" :data-diff-state="project.state">
    {{ visibleText(project.message) }}
  </p>
  <p v-else-if="project.message" class="dhead__note" data-diff-message>{{ visibleText(project.message) }}</p>
</template>

<style scoped>
.dhead {
  display: grid;
  gap: var(--omo-space);
}
.dhead__name {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.dhead__sum {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.dhead__sum code {
  color: var(--omo-fg);
}
.dhead__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.dhead__note--error {
  color: var(--omo-danger);
}
.dhead__line {
  margin: 0;
}
.dhead__remote {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border-left: var(--omo-edge) solid var(--omo-warn);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
</style>
