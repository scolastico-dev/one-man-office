<script setup lang="ts">
// The ticket's fields as values to read: what the panel shows beside the
// description until the fields can be edited there.
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import type { TicketSummary } from '../../api/types'
import { useBoard } from '../../store'
import { COLUMN_TITLES, ROLE_LABELS } from '../board/model'

const props = defineProps<{ ticket: TicketSummary }>()

const store = useBoard()
const route = useRoute()

const REVIEW: Record<string, string> = { skipped: 'Skipped', agent: 'Agent', human: 'Human', agent_then_human: 'Agent, then human' }
const WHEN: Record<string, string> = { merge: 'Merge locally', export: 'Export as a pull request' }
const RELEASE: Record<string, string> = { agents: 'Agents', user: 'You' }

const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
const date = (s: string | null) => (s ? fmt.format(new Date(s)) : '')

function ref(n: number) {
  const t = store.view(n)
  return { n, text: t ? `#${n} ${t.title}` : `#${n}`, title: t ? `${COLUMN_TITLES[t.column]}` : undefined }
}

const rows = computed(() => {
  const t = props.ticket
  const out: { key: string; label: string; value?: string; links?: ReturnType<typeof ref>[]; lines?: string[] }[] = []
  out.push({
    key: 'projects',
    label: t.projects.length > 1 ? 'Projects' : 'Project',
    lines:
      t.projects.length === 0
        ? ['None']
        : t.projects.map((p) => {
            const name = store.state.projects.get(p.project)?.name ?? p.project
            const parts = [name]
            if (p.base_branch) parts.push(`from ${p.base_branch}`)
            if (p.branch) parts.push(`branch ${p.branch}`)
            return parts.join(', ')
          }),
  })
  if (t.type === 'task') out.push({ key: 'story', label: 'Story', ...(t.story === null ? { value: 'None' } : { links: [ref(t.story)] }) })
  out.push({ key: 'depends', label: 'Depends on', ...(t.depends_on.length === 0 ? { value: 'Nothing' } : { links: t.depends_on.map(ref) }) })
  out.push({ key: 'blocks', label: 'Blocks', ...(t.blocks.length === 0 ? { value: 'Nothing' } : { links: t.blocks.map(ref) }) })
  out.push({ key: 'supervisor', label: 'Supervisor', value: ROLE_LABELS[t.supervisor] })
  out.push({ key: 'model', label: 'Model', value: t.model || 'Default' })
  out.push({ key: 'review', label: 'Review', value: `${REVIEW[t.effective_review] ?? t.effective_review}${t.review === null ? ' (default)' : ''}` })
  if (!(t.type === 'task' && t.story !== null)) {
    out.push({ key: 'when', label: 'When approved', value: `${WHEN[t.effective_when_approved] ?? t.effective_when_approved}${t.when_approved === null ? ' (default)' : ''}` })
  }
  if (t.type === 'story') {
    const r = t.effective_task_release
    out.push({ key: 'release', label: 'Tasks released by', value: r ? `${RELEASE[r] ?? r}${t.task_release === null ? ' (default)' : ''}` : 'Default' })
    out.push({ key: 'parallel', label: 'Tasks at once', value: t.max_parallel === null ? 'No limit' : String(t.max_parallel) })
  }
  out.push({ key: 'created', label: 'Created', value: date(t.created_at) })
  out.push({ key: 'updated', label: 'Updated', value: date(t.updated_at) })
  if (t.closed_at) out.push({ key: 'closed', label: 'Closed', value: date(t.closed_at) })
  return out
})
</script>

<template>
  <dl class="facts" data-ticket-facts>
    <div v-for="r in rows" :key="r.key" class="facts__row" :data-fact="r.key">
      <dt class="facts__label">{{ r.label }}</dt>
      <dd class="facts__value">
        <template v-if="r.links">
          <RouterLink
            v-for="l in r.links"
            :key="l.n"
            :to="{ path: `/${l.n}`, query: route.query }"
            :title="l.title"
            class="facts__link"
          >{{ l.text }}</RouterLink>
        </template>
        <template v-else-if="r.lines">
          <span v-for="(line, i) in r.lines" :key="i" class="facts__line">{{ line }}</span>
        </template>
        <template v-else>{{ r.value }}</template>
      </dd>
    </div>
  </dl>
</template>

<style scoped>
.facts {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
}
.facts__row {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.facts__label {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.facts__value {
  display: grid;
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.facts__link {
  color: var(--omo-accent);
}
</style>
