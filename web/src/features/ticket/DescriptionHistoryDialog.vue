<script setup lang="ts">
// The versions of a ticket's description, newest first, with a rendered
// preview of the one selected and a way to restore it. Restoring adds a new
// version that says which one it came from; nothing is deleted. Text and
// author names come from agents and are shown the way the description is.
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { getDescriptions, restoreDescription } from '../../api/endpoints'
import { failureText, isLostAnswer } from '../../api/lostAnswer'
import type { DescriptionChangedData, DescriptionVersion, TicketSummary } from '../../api/types'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { ROLE_LABELS } from '../board/model'
import DescriptionView from './DescriptionView.vue'

const props = defineProps<{
  open: boolean
  ticket: TicketSummary
  /** The description is being edited in the panel. */
  editing: boolean
}>()
const emit = defineEmits<{
  close: []
  /** Restored: the new version, and the event that carries it (null when the answer had none). */
  restored: [version: DescriptionVersion, eventId: number | null]
}>()

const session = useSession()
const store = useBoard()
const say = useAnnouncer()

const versions = shallowRef<DescriptionVersion[]>([])
const loaded = ref(false)
const loadError = ref('')
const selected = ref<number | null>(null)
const restoring = ref(false)
const askFirst = ref(false)
const error = ref('')

const done = computed(() => props.ticket.column === 'done')
const shown = computed(() => versions.value.find((v) => v.number === selected.value) ?? null)

let token = 0
async function load() {
  const my = ++token
  try {
    const list = [...(await getDescriptions(session.client, props.ticket.number)).data].sort((a, b) => b.number - a.number)
    if (my !== token) return
    versions.value = list
    loadError.value = ''
    loaded.value = true
    // Keep the selection while it exists; else the current version.
    if (!list.some((v) => v.number === selected.value)) selected.value = (list.find((v) => v.current) ?? list[0])?.number ?? null
  } catch (e) {
    if (my !== token) return
    loadError.value = e instanceof Error && e.message !== '' ? e.message : 'The versions could not be loaded.'
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    loaded.value = false
    selected.value = null
    askFirst.value = false
    error.value = ''
    void load()
  },
  { immediate: true },
)

const off = store.onEvent('description.changed', (ev) => {
  const d = ev.data as DescriptionChangedData | null
  if (props.open && d?.ticket === props.ticket.number) void load()
})
onBeforeUnmount(() => {
  token++
  off()
})

function select(n: number) {
  selected.value = n
  askFirst.value = false
  error.value = ''
}

function restore() {
  const v = shown.value
  if (v === null || v.current || done.value) return
  if (props.editing && !askFirst.value) {
    askFirst.value = true
    return
  }
  void send(v)
}

async function send(v: DescriptionVersion) {
  if (restoring.value) return
  restoring.value = true
  error.value = ''
  askFirst.value = false
  try {
    const res = await restoreDescription(session.client, props.ticket.number, v.number)
    say.polite(`Version ${v.number} of the description of #${props.ticket.number} restored as version ${res.data.number}.`)
    selected.value = res.data.number
    emit('restored', res.data, res.eventId)
    await load()
  } catch (e) {
    error.value = failureText(e, {
      lost: `No answer from omo, so restoring version ${v.number} may have happened. The list shows what omo did.`,
      refused: 'omo refused it.',
    })
    say.assertive(error.value)
    if (isLostAnswer(e)) await load()
  } finally {
    restoring.value = false
  }
}

function who(v: DescriptionVersion): string {
  const role = ROLE_LABELS[v.author.role] ?? v.author.role
  return v.author.name ? `${role} ${v.author.name}` : role
}

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}
</script>

<template>
  <UiDialog :open="open" :title="`Description history of #${ticket.number}`" :eyebrow="ticket.title" wide @close="emit('close')">
    <div class="hist" data-history-dialog>
      <p v-if="loadError" class="hist__error" role="alert" data-history-load-error>
        {{ loadError }}
        <UiButton size="sm" @click="load">Try again</UiButton>
      </p>
      <p v-else-if="!loaded" class="hist__muted" aria-live="polite">Loading the versions…</p>
      <p v-else-if="versions.length === 0" class="hist__muted" data-history-empty>There is no description yet.</p>
      <div v-else class="hist__grid">
        <ul class="hist__list" aria-label="Versions, newest first" data-history-list>
          <li v-for="v in versions" :key="v.number">
            <button
              type="button"
              class="hist__item"
              :aria-pressed="v.number === selected"
              :data-version="v.number"
              @click="select(v.number)"
            >
              <span class="hist__head">
                <span class="hist__number">Version {{ v.number }}</span>
                <span v-if="v.current" class="hist__mark" data-current-mark>current</span>
              </span>
              <span class="hist__meta">{{ who(v) }}</span>
              <span class="hist__meta">{{ when(v.created_at) }}</span>
              <span v-if="v.restored_from !== null" class="hist__meta" data-restored-from>restored from v{{ v.restored_from }}</span>
            </button>
          </li>
        </ul>
        <section v-if="shown" class="hist__preview" :aria-label="`Version ${shown.number}`" data-history-preview>
          <DescriptionView v-if="shown.text.trim() !== ''" :text="shown.text" />
          <p v-else class="hist__muted">This version is empty.</p>
        </section>
      </div>
      <div v-if="askFirst" class="hist__ask" role="alert" data-restore-ask>
        <p class="hist__text">Restoring replaces the text you are editing. Restore anyway?</p>
        <div class="hist__row">
          <UiButton size="sm" variant="danger" :disabled="restoring" data-restore-anyway @click="shown && send(shown)">Restore anyway</UiButton>
          <UiButton size="sm" data-restore-keep @click="askFirst = false">Keep editing</UiButton>
        </div>
      </div>
      <p v-if="error" class="hist__error" role="alert" data-history-error>{{ error }}</p>
      <p v-if="done" class="hist__muted" data-history-readonly>#{{ ticket.number }} is done; done tickets are read-only.</p>
    </div>
    <template #actions>
      <UiButton
        :disabled="shown === null || shown.current || done || restoring"
        data-restore
        @click="restore"
      >
        Restore this version
      </UiButton>
      <UiButton variant="primary" data-history-done @click="emit('close')">Done</UiButton>
    </template>
  </UiDialog>
</template>

<style scoped>
.hist {
  display: grid;
  gap: calc(var(--omo-space) * 3);
}
.hist__grid {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  grid-template-columns: minmax(0, 14rem) minmax(0, 1fr);
  align-items: start;
}
@media (max-width: 640px) {
  .hist__grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
.hist__list {
  display: grid;
  gap: var(--omo-space);
  max-height: 50dvh;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;
}
.hist__item {
  display: grid;
  gap: calc(var(--omo-space) / 2);
  width: 100%;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
  color: var(--omo-fg);
  text-align: left;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.hist__item[aria-pressed='true'] {
  border-color: var(--omo-accent);
  border-left-width: var(--omo-edge);
  background: var(--omo-raised);
}
.hist__head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: calc(var(--omo-space) * 2);
}
.hist__number {
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.hist__mark {
  color: var(--omo-accent);
  font-size: var(--omo-text-xs);
  font-weight: 700;
  line-height: var(--omo-text-xs-lh);
}
.hist__meta {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.hist__preview {
  min-width: 0;
  max-height: 50dvh;
  overflow: auto;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
}
.hist__text,
.hist__muted,
.hist__error {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.hist__muted {
  color: var(--omo-muted);
}
.hist__error,
.hist__ask {
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
}
.hist__row {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--omo-space) * 2);
  margin-top: calc(var(--omo-space) * 2);
}
</style>
