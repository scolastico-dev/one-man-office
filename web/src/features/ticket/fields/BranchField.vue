<script setup lang="ts">
// The branch of each project of the ticket, and beside it where its export
// stands: the pull request's link, being exported, failed with the reason,
// left out (nothing to export) or no pull request after a failed export.
// A project omo made a branch or worktree for can be taken off the ticket
// from here ("Remove from ticket"), after a confirmation.
import { computed, ref } from 'vue'
import type { TicketProject, TicketSummary } from '../../../api/types'
import UiButton from '../../../components/ui/UiButton.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { useBoard } from '../../../store'
import FieldRow from '../FieldRow.vue'
import RemoveProjectDialog from '../RemoveProjectDialog.vue'
import { linkOf, projectExport, projectExportWords } from '../exportState'
import { canRemoveProject } from '../removeProject'

const props = defineProps<{ ticket: TicketSummary }>()
const store = useBoard()
const nameOf = (id: string) => store.state.projects.get(id)?.name ?? id
const exportOf = (project: string) => projectExport(props.ticket, project)

/** The project whose removal is being confirmed. */
const removing = ref<string | null>(null)
const removingProject = computed<TicketProject | null>(() => props.ticket.projects.find((p) => p.project === removing.value) ?? null)
</script>

<template>
  <FieldRow v-if="ticket.projects.length > 0" field="branch" :label="ticket.projects.length > 1 ? 'Branches' : 'Branch'" :editable="false" :editing="false">
    <template #value>
      <span v-for="p in ticket.projects" :key="p.project" class="branch__line" :data-branch="p.project">
        <span v-if="ticket.projects.length > 1" class="branch__project">{{ nameOf(p.project) }}</span>
        <code v-if="p.branch !== ''" class="branch__name">{{ p.branch }}</code>
        <span v-else>Not created yet</span>
        <a v-if="linkOf(p.export_url)" :href="linkOf(p.export_url)" class="branch__pr" target="_blank" rel="noreferrer noopener" :data-export-state="'pull_request'"
          >Pull request{{ ticket.projects.length > 1 ? ` for ${nameOf(p.project)}` : '' }}</a
        >
        <span
          v-else-if="exportOf(p.project)"
          class="branch__export"
          :class="{ 'branch__export--failed': exportOf(p.project)!.kind === 'failed' }"
          :data-export-state="exportOf(p.project)!.kind"
          >{{ visibleText(projectExportWords(exportOf(p.project)!)) }}</span
        >
        <span v-if="canRemoveProject(ticket, p)" class="branch__remove">
          <UiButton size="sm" variant="ghost" :data-remove-project="p.project" @click="removing = p.project">Remove from ticket</UiButton>
        </span>
      </span>
      <RemoveProjectDialog
        v-if="removingProject"
        :open="removingProject !== null"
        :ticket="ticket"
        :project="removingProject"
        @close="removing = null"
      />
    </template>
  </FieldRow>
</template>

<style scoped>
.branch__line {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
}
.branch__project {
  color: var(--omo-muted);
}
.branch__name {
  font: inherit;
  overflow-wrap: anywhere;
}
.branch__pr {
  color: var(--omo-accent);
}
.branch__export {
  color: var(--omo-muted);
  overflow-wrap: anywhere;
}
.branch__export--failed {
  color: var(--omo-danger);
}
.branch__remove {
  justify-self: start;
}
</style>
