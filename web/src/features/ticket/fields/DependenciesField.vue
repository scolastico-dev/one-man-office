<script setup lang="ts">
// What this ticket depends on: each ticket with its number, title and column
// as a link that opens it, why it is not satisfied, and one action to remove
// it; and a search to add one. The search offers the tickets the rules could
// accept; the server still has the last word and its message is shown as it is.
import { computed, ref, shallowRef, useId } from 'vue'
import { addDependency, removeDependency } from '../../../api/endpoints'
import { failureText } from '../../../api/lostAnswer'
import type { TicketSummary } from '../../../api/types'
import UiButton from '../../../components/ui/UiButton.vue'
import { useAnnouncer } from '../../../components/ui/useAnnouncer'
import { useSession } from '../../../session'
import { useBoard } from '../../../store'
import { COLUMN_TITLES } from '../../board/model'
import FieldRow from '../FieldRow.vue'
import { addDependencyState, dependencyCandidates, matchCandidates, unsatisfiedWords, type FieldState } from '../fieldRules'
import TicketLink from '../TicketLink.vue'
import { vDisabled } from '../../../components/ui/vDisabled'

const props = defineProps<{ ticket: TicketSummary; state: FieldState }>()
const store = useBoard()
const session = useSession()
const say = useAnnouncer()
const id = useId()

const query = ref('')
const busy = ref(false)
/** Numbers whose removal is under way. */
const removing = ref<number[]>([])
const error = ref('')
/** Changes shown until the stream has delivered them: number -> event id. */
const added = shallowRef(new Map<number, number>())
const removed = shallowRef(new Map<number, number>())

const live = (m: Map<number, number>, n: number) => {
  const at = m.get(n)
  return at !== undefined && store.state.lastEventId < at
}

const shown = computed(() => {
  const out = props.ticket.depends_on.filter((n) => !live(removed.value, n))
  for (const n of added.value.keys()) if (live(added.value, n) && !out.includes(n)) out.push(n)
  return out
})

const info = (n: number) => {
  const t = store.view(n)
  return { n, text: t ? `#${n} ${t.title}` : `#${n}`, title: t?.title ?? '', column: t ? COLUMN_TITLES[t.column] : '', why: unsatisfiedWords(props.ticket, n) }
}

const rule = computed(() => addDependencyState(props.ticket, (n) => store.tasksOf(n)))
const candidates = computed(() =>
  dependencyCandidates(
    { ...props.ticket, depends_on: shown.value },
    [...store.state.tickets.values()].map((e) => e.t),
  ),
)
const MAX_RESULTS = 8
const results = computed(() => (query.value.trim() === '' ? [] : matchCandidates(candidates.value, query.value).slice(0, MAX_RESULTS)))
/** A number the rules do not offer can still be tried: omo says why not. */
const typed = computed(() => {
  const m = /^#?(\d{1,9})$/u.exec(query.value.trim())
  const n = m ? Number(m[1]) : 0
  if (n < 1 || n === props.ticket.number || shown.value.includes(n) || candidates.value.some((c) => c.number === n)) return 0
  return n
})

function remember(map: typeof added, n: number, eventId: number | null) {
  if (eventId === null || store.state.lastEventId >= eventId) return
  const next = new Map(map.value)
  next.set(n, eventId)
  map.value = next
}

async function add(n: number) {
  if (busy.value || !rule.value.allowed) return
  busy.value = true
  error.value = ''
  try {
    const res = await addDependency(session.client, props.ticket.number, n)
    remember(added, n, res.eventId)
    const dropped = new Map(removed.value)
    dropped.delete(n)
    removed.value = dropped
    query.value = ''
    say.polite(`#${props.ticket.number} now depends on #${n}.`)
  } catch (e) {
    error.value = failureText(e, {
      lost: `No answer from omo, so the dependency on #${n} may have been added. The list shows what omo has.`,
      refused: `omo did not accept the dependency on #${n}.`,
    })
    say.assertive(error.value)
  } finally {
    busy.value = false
  }
}

async function remove(n: number) {
  if (removing.value.includes(n)) return
  removing.value = [...removing.value, n]
  error.value = ''
  try {
    const res = await removeDependency(session.client, props.ticket.number, n)
    remember(removed, n, res.eventId)
    // An addition still shown from this page is over.
    if (added.value.has(n)) {
      const kept = new Map(added.value)
      kept.delete(n)
      added.value = kept
    }
    say.polite(`#${props.ticket.number} no longer depends on #${n}.`)
  } catch (e) {
    error.value = failureText(e, {
      lost: `No answer from omo, so the dependency on #${n} may have been removed. The list shows what omo has.`,
      refused: `omo did not remove the dependency on #${n}.`,
    })
    say.assertive(error.value)
  } finally {
    removing.value = removing.value.filter((x) => x !== n)
  }
}

function onEnter() {
  const first = results.value[0]
  if (first) void add(first.number)
  else if (typed.value > 0) void add(typed.value)
}
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.isComposing) {
    e.preventDefault()
    onEnter()
  } else if (e.key === 'Escape' && query.value !== '') {
    e.preventDefault()
    e.stopPropagation()
    query.value = ''
  }
}
</script>

<template>
  <FieldRow field="depends_on" label="Depends on" :editable="false" :locked-reason="state.lockedReason" :editing="false" :error="error">
    <template #value>
      <span v-if="shown.length === 0">Nothing</span>
      <ul v-else class="deps" role="list">
        <li v-for="d in shown.map(info)" :key="d.n" class="dep" :data-dependency="d.n">
          <div class="dep__main">
            <TicketLink class="dep__link" :number="d.n">{{ d.text }}</TicketLink>
            <span v-if="d.column" class="dep__column">{{ d.column }}</span>
            <span v-if="d.why" class="dep__why" data-unsatisfied>{{ d.why }}</span>
          </div>
          <UiButton
            v-if="state.editable"
            size="sm"
            class="dep__remove"
            :disabled="removing.includes(d.n)"
            :aria-label="`Remove the dependency on #${d.n}${d.title ? ' ' + d.title : ''}`"
            data-remove-dependency
            @click="remove(d.n)"
          >
            Remove
          </UiButton>
        </li>
      </ul>
    </template>
    <template v-if="state.editable" #after>
      <div class="add" data-add-dependency>
        <p v-if="!rule.allowed" class="add__lock" data-locked>{{ rule.reason }}</p>
        <template v-else>
          <label :for="`${id}-add`" class="omo-field__label">Add a dependency</label>
          <input
            :id="`${id}-add`"
            v-model="query"
            class="omo-input"
            type="search"
            autocomplete="off"
            placeholder="Number or title"
            :disabled="busy"
            :aria-describedby="`${id}-hint`"
            @keydown="onKeydown"
          />
          <p :id="`${id}-hint`" class="omo-field__hint">Offers the tickets this one can depend on. Enter adds the first.</p>
          <ul v-if="results.length > 0 || typed > 0" class="add__results" role="list" :aria-label="`Tickets matching ${query}`">
            <li v-for="c in results" :key="c.number">
              <button type="button" class="add__pick" v-disabled="busy" :data-candidate="c.number" @click="add(c.number)">
                <span>#{{ c.number }} {{ c.title }}</span>
                <span class="dep__column">{{ COLUMN_TITLES[c.column] }}</span>
              </button>
            </li>
            <li v-if="typed > 0">
              <button type="button" class="add__pick" v-disabled="busy" data-candidate-anyway @click="add(typed)">
                <span>Try #{{ typed }} anyway</span>
                <span class="dep__column">omo is not offering it; it will say why if it refuses</span>
              </button>
            </li>
          </ul>
          <p v-else-if="query.trim() !== ''" class="omo-field__hint" data-no-candidates>No ticket that this one could depend on matches.</p>
        </template>
      </div>
    </template>
  </FieldRow>
</template>

<style scoped>
.deps {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  margin: 0;
  padding: 0;
  list-style: none;
}
.dep {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--omo-space) calc(var(--omo-space) * 2);
}
.dep__main {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  min-width: 0;
}
.dep__link {
  color: var(--omo-accent);
}
.dep__column {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.dep__why {
  color: var(--omo-warn);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.add {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.add__lock {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.add__results {
  display: grid;
  gap: var(--omo-space);
  margin: 0;
  padding: 0;
  list-style: none;
}
.add__pick {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  width: 100%;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 2);
  border: var(--omo-border) solid var(--omo-line);
  background: none;
  color: var(--omo-fg);
  font: inherit;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-align: left;
  overflow-wrap: anywhere;
  cursor: pointer;
}
.add__pick:is(:disabled, [aria-disabled='true']) {
  color: var(--omo-muted);
  cursor: not-allowed;
}
@media (hover: hover) {
  .add__pick:hover:not(:disabled, [aria-disabled='true']) {
    border-color: var(--omo-accent);
  }
}
</style>
