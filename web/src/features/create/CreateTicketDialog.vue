<script setup lang="ts">
// The "Create ticket" dialog: one form for a story or a task. Only the
// server judges a request; the dialog checks what is cheap and certain,
// shows the server's own message next to the field it concerns (or at the
// top) and keeps everything the user typed. A created ticket appears on the
// board through the event stream, never from the response.
import { computed, nextTick, onBeforeUnmount, reactive, ref, useId, watch } from 'vue'
import { useRouter } from 'vue-router'
import { openTicketLocation } from '../ticket/openTicket'
import { createTicket, listModels } from '../../api/endpoints'
import type { Model, Review, Role, TaskRelease, WhenApproved } from '../../api/types'
import SelectField, { type SelectOption } from '../../components/ui/SelectField.vue'
import TextArea from '../../components/ui/TextArea.vue'
import TextField from '../../components/ui/TextField.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useToast } from '../../components/ui/useToast'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import {
  buildRequest,
  emptyForm,
  inStory,
  isDirty,
  withoutProject,
  MORE_FIELDS,
  problemOf,
  STORY_TEMPLATE,
  storiesForTasks,
  TITLE_MAX,
  type CreateForm,
  type FieldKey,
} from './createRules'
import ProjectPicker from './ProjectPicker.vue'
import { defaultKind } from '../ticket/fieldRules'

const props = defineProps<{
  open: boolean
  /** Story preset for a new task. */
  initialStory?: number
}>()
const emit = defineEmits<{ close: []; created: [n: number] }>()

const session = useSession()
const store = useBoard()
const router = useRouter()
const toast = useToast()
const announce = useAnnouncer()
const formId = useId()
const root = ref<HTMLElement>()

const form = reactive<CreateForm>(emptyForm())
const errors = reactive<Partial<Record<FieldKey, string>>>({})
const topError = ref('')
const topEl = ref<HTMLElement>()
const busy = ref(false)
const another = ref(false)
const moreOpen = ref(false)
const confirming = ref(false)
const models = ref<Model[]>([])

// Models are read each time the dialog opens, so a changed setup shows up.
let loadId = 0
async function loadModels() {
  const id = ++loadId
  try {
    const { data } = await listModels(session.client)
    if (id === loadId) models.value = data
  } catch {
    if (id === loadId) models.value = []
  }
}

function clearErrors() {
  for (const k of Object.keys(errors)) delete errors[k as FieldKey]
  topError.value = ''
}

const isTask = computed(() => form.type === 'task')
const general = computed(() => store.state.general)

const usableProjects = computed(() => [...store.state.projects.values()].filter((p) => p.enabled && !p.missing))
const stories = computed(() => storiesForTasks([...store.state.tickets.values()].map((e) => e.t)))
const storyTicket = computed(() => (isTask.value && form.story !== null ? store.view(form.story) : undefined))
/** Project ids a task in the chosen story may use; null when the choice is not limited. */
const allowedProjects = computed(() => (storyTicket.value ? storyTicket.value.projects.map((p) => p.project) : null))

/** A single usable project is the obvious choice; so is a story's only project. */
function preselect() {
  if (form.projects.length > 0 || withoutProject(form)) return
  const pool = allowedProjects.value ?? usableProjects.value.map((p) => p.id)
  const usable = pool.filter((id) => usableProjects.value.some((p) => p.id === id))
  if (usable.length === 1 && usable[0] !== undefined) form.projects = [usable[0]]
}

watch(
  () => props.open,
  (open) => {
    if (!open) return
    Object.assign(form, emptyForm(props.initialStory))
    clearErrors()
    busy.value = false
    another.value = false
    moreOpen.value = false
    confirming.value = false
    void loadModels()
    preselect()
  },
  { immediate: true },
)
onBeforeUnmount(() => loadId++)

// Typing in a field removes the message that was about it.
for (const [key, source] of [
  ['title', () => form.title],
  ['description', () => form.description],
  ['projects', () => `${form.projects.join()}|${form.noProject}`],
  ['story', () => form.story],
  ['supervisor', () => form.supervisor],
  ['model', () => form.model],
  ['review', () => form.review],
  ['when_approved', () => form.whenApproved],
  ['task_release', () => form.taskRelease],
  ['max_parallel', () => form.maxParallel],
  ['depends_on', () => form.dependsOn],
] as [FieldKey, () => unknown][]) {
  watch(source, () => delete errors[key])
}

watch([usableProjects, allowedProjects], () => {
  if (!props.open) return
  // A story's projects limit a task's choice.
  const allowed = allowedProjects.value
  if (isTask.value && allowed !== null && form.projects.some((id) => !allowed.includes(id))) form.projects = []
  preselect()
})

function setType(type: 'story' | 'task') {
  if (form.type === type) return
  form.type = type
  if (type === 'story') form.noProject = false
  if (type === 'task') {
    if (form.description.trim() === STORY_TEMPLATE.trim()) form.description = ''
    form.projects = form.projects.slice(0, 1)
  } else if (form.description.trim() === '') {
    form.description = STORY_TEMPLATE
  }
  form.supervisor = ''
  preselect()
}

const storyOptions = computed<SelectOption[]>(() => [
  { value: '', label: 'No story: a standalone task' },
  ...stories.value.map((s) => ({ value: String(s.number), label: `#${s.number} ${s.title}` })),
])
const storyValue = computed({
  get: () => (form.story === null ? '' : String(form.story)),
  set: (v: string) => {
    form.story = v === '' ? null : Number(v)
    if (form.supervisor === 'pm' && v === '') form.supervisor = ''
  },
})

const REVIEW_LABEL: Record<Review, string> = { skipped: 'Skipped', agent: 'Agent', human: 'Human', agent_then_human: 'Agent then human' }
const APPROVED_LABEL: Record<WhenApproved, string> = { merge: 'Merge locally', export: 'Export as pull request' }
const RELEASE_LABEL: Record<TaskRelease, string> = { agents: 'Agents release tasks', user: 'I release tasks' }

const reviewOptions = computed<SelectOption[]>(() => {
  const effective = general.value?.review_defaults[defaultKind({ type: form.type, story: isTask.value ? form.story : null })]
  return [
    { value: '', label: effective ? `Default (${REVIEW_LABEL[effective]})` : 'Default' },
    ...(Object.keys(REVIEW_LABEL) as Review[]).map((r) => ({ value: r, label: REVIEW_LABEL[r] })),
  ]
})
const approvedOptions = computed<SelectOption[]>(() => {
  const effective = general.value?.when_approved_defaults[defaultKind({ type: form.type, story: isTask.value ? form.story : null })]
  return [
    { value: '', label: effective ? `Default (${APPROVED_LABEL[effective]})` : 'Default' },
    ...(Object.keys(APPROVED_LABEL) as WhenApproved[]).map((w) => ({ value: w, label: APPROVED_LABEL[w] })),
  ]
})
const releaseOptions = computed<SelectOption[]>(() => {
  const effective: TaskRelease | null = general.value ? (general.value.tasks.require_user_release ? 'user' : 'agents') : null
  return [
    { value: '', label: effective ? `Default (${RELEASE_LABEL[effective]})` : 'Default' },
    ...(Object.keys(RELEASE_LABEL) as TaskRelease[]).map((r) => ({ value: r, label: RELEASE_LABEL[r] })),
  ]
})
const supervisorOptions = computed<SelectOption[]>(() => {
  // Left empty the server picks: the story's product manager for a task in a story, otherwise the creator (you).
  const out: SelectOption[] = [
    { value: '', label: `Default (${inStory(form) ? 'Product manager' : 'Me'})` },
    { value: 'user', label: 'Me' },
  ]
  if (general.value?.ceo.enabled) out.push({ value: 'ceo', label: 'CEO' })
  if (inStory(form)) out.push({ value: 'pm', label: 'Product manager' })
  return out
})
const modelOptions = computed<SelectOption[]>(() => [
  { value: '', label: "Default (the role's model)" },
  ...models.value.map((m) => ({ value: m.id, label: m.selectable ? m.id : `${m.id} (not selectable by agents)` })),
])
const modelHint = computed(() => models.value.find((m) => m.id === form.model)?.description || undefined)

const typed = <T extends string>(v: string) => v as T | ''

const titleCount = computed(() => `${[...form.title].length}/${TITLE_MAX}`)
const projectsLabel = computed(() => (isTask.value ? 'Project' : 'Projects'))
const projectsHint = computed(() => {
  if (!isTask.value) return 'A story needs a project before it can be approved.'
  if (withoutProject(form)) return 'Approving it merges and exports nothing; omo keeps its folder until you remove it. Code changes to a project need a task with that project.'
  return storyTicket.value ? `A task in a story uses one of the story's projects (#${storyTicket.value.number}), or none.` : undefined
})

async function focusField(key: FieldKey | null) {
  await nextTick()
  const host = root.value
  if (!host) return
  if (key === null) {
    topEl.value?.focus()
    return
  }
  host.querySelector<HTMLElement>(`[data-field="${key}"] :is(input, select, textarea):not([disabled])`)?.focus()
}

function show(key: FieldKey | null, message: string) {
  if (key === null) topError.value = message
  else {
    errors[key] = message
    if (MORE_FIELDS.includes(key)) moreOpen.value = true
  }
  announce.assertive(message)
  void focusField(key)
}

async function submit() {
  if (busy.value) return
  clearErrors()
  const built = buildRequest(form)
  if ('field' in built) {
    show(built.field, built.message)
    return
  }
  busy.value = true
  try {
    const { data } = await createTicket(session.client, built.request)
    const n = data.number
    toast.show({
      title: `#${n} created`,
      message: data.title,
      action: { label: 'Open', run: () => void router.push(openTicketLocation(store, n, router.currentRoute.value)) },
    })
    emit('created', n)
    if (another.value) {
      form.title = ''
      form.description = form.type === 'story' ? STORY_TEMPLATE : ''
      form.dependsOn = ''
      announce.polite(`#${n} created. The form is ready for the next ticket.`)
      void focusField('title')
    } else {
      emit('close')
    }
  } catch (e) {
    const p = problemOf(e, undefined, 'No answer from omo, so the ticket may have been created. Look for it in Planning before you press Create again.')
    show(p.field, p.message)
  } finally {
    busy.value = false
  }
}

/** Closing asks first when something was typed. */
function requestClose() {
  if (busy.value) return
  if (confirming.value) {
    confirming.value = false
    return
  }
  if (isDirty(form)) {
    confirming.value = true
    void nextTick(() => root.value?.closest('dialog')?.querySelector<HTMLElement>('[data-keep-editing]')?.focus())
    return
  }
  emit('close')
}

function keepEditing() {
  confirming.value = false
  void focusField('title')
}
</script>

<template>
  <UiDialog :open="open" title="Create ticket" eyebrow="board" @close="requestClose">
    <div ref="root">
      <p v-if="topError" ref="topEl" class="create__error" role="alert" tabindex="-1" data-form-error>{{ topError }}</p>
      <form :id="formId" class="create__form" novalidate :aria-busy="busy" @submit.prevent="submit">
        <fieldset class="create__type">
          <legend class="omo-field__label">Type</legend>
          <div class="create__segments">
            <label class="create__segment">
              <input type="radio" class="sr-only" name="type" value="story" :checked="form.type === 'story'" @change="setType('story')" />
              <span>Story</span>
            </label>
            <label class="create__segment">
              <input type="radio" class="sr-only" name="type" value="task" :checked="form.type === 'task'" @change="setType('task')" />
              <span>Task</span>
            </label>
          </div>
          <p class="omo-field__hint">
            {{ isTask ? 'A task is one piece of work in one project, or in none.' : 'A story is a goal that is split into tasks.' }}
          </p>
        </fieldset>

        <div data-field="title">
          <TextField v-model="form.title" label="Title" name="title" autocomplete="off" autofocus :hint="titleCount" :error="errors.title" />
        </div>

        <div v-if="isTask" data-field="story">
          <SelectField
            v-model="storyValue"
            label="Story (optional)"
            name="story"
            :options="storyOptions"
            hint="Only stories that can still take tasks are listed."
            :error="errors.story"
          />
        </div>

        <div data-field="projects">
          <ProjectPicker
            v-model="form.projects"
            v-model:none="form.noProject"
            :label="projectsLabel"
            :multiple="!isTask"
            :none-option="isTask"
            :only="allowedProjects"
            :notice="!inStory(form)"
            :hint="projectsHint"
            :error="errors.projects"
          />
        </div>

        <div data-field="description">
          <TextArea
            v-model="form.description"
            label="Description"
            name="description"
            :rows="isTask ? 5 : 10"
            placeholder="Leave empty to start from the Goal / Do / Don't / Done when template."
            :hint="
              (isTask ? 'Markdown.' : 'Markdown. The headings Goal, Do, Don\'t and Done when are a template; keep what helps.') +
              ' Images can be added once the ticket exists: edit its description or comment on it.'
            "
            :error="errors.description"
          />
        </div>

        <details class="create__more" :open="moreOpen" @toggle="moreOpen = ($event.target as HTMLDetailsElement).open">
          <summary>More options</summary>
          <div class="create__more-body">
            <div data-field="supervisor">
              <SelectField
                :model-value="form.supervisor"
                label="Supervisor"
                name="supervisor"
                :options="supervisorOptions"
                :error="errors.supervisor"
                @update:model-value="form.supervisor = typed<Role>($event)"
              />
            </div>
            <div data-field="model">
              <SelectField v-model="form.model" label="Model" name="model" :options="modelOptions" :hint="modelHint" :error="errors.model" />
            </div>
            <div data-field="review">
              <SelectField
                :model-value="form.review"
                label="Review"
                name="review"
                :options="reviewOptions"
                :error="errors.review"
                @update:model-value="form.review = typed<Review>($event)"
              />
            </div>
            <div v-if="!inStory(form) && !withoutProject(form)" data-field="when_approved">
              <SelectField
                :model-value="form.whenApproved"
                label="When approved"
                name="when_approved"
                :options="approvedOptions"
                :error="errors.when_approved"
                @update:model-value="form.whenApproved = typed<WhenApproved>($event)"
              />
            </div>
            <template v-if="!isTask">
              <div data-field="task_release">
                <SelectField
                  :model-value="form.taskRelease"
                  label="Task release"
                  name="task_release"
                  :options="releaseOptions"
                  hint="Who sets the story's tasks to Ready For Dev."
                  :error="errors.task_release"
                  @update:model-value="form.taskRelease = typed<TaskRelease>($event)"
                />
              </div>
              <div data-field="max_parallel">
                <TextField
                  v-model="form.maxParallel"
                  label="Max parallel tasks"
                  name="max_parallel"
                  inputmode="numeric"
                  autocomplete="off"
                  hint="Leave empty for no limit of its own."
                  :error="errors.max_parallel"
                />
              </div>
            </template>
            <div data-field="depends_on">
              <TextField
                v-model="form.dependsOn"
                label="Depends on"
                name="depends_on"
                autocomplete="off"
                placeholder="12, 15"
                hint="Ticket numbers separated by commas."
                :error="errors.depends_on"
              />
            </div>
          </div>
        </details>
      </form>
    </div>

    <template #actions>
      <template v-if="confirming">
        <p class="create__discard" role="alert">Discard what you typed?</p>
        <UiButton data-keep-editing variant="primary" @click="keepEditing">Keep editing</UiButton>
        <UiButton variant="danger" @click="emit('close')">Discard</UiButton>
      </template>
      <template v-else>
        <label class="create__another"><input v-model="another" type="checkbox" /> Create another</label>
        <UiButton :disabled="busy" @click="requestClose">Cancel</UiButton>
        <UiButton type="submit" variant="primary" data-create-submit :form="formId" :disabled="busy">{{ busy ? 'Creating…' : 'Create' }}</UiButton>
      </template>
    </template>
  </UiDialog>
</template>

<style scoped>
.create__form {
  display: grid;
  gap: calc(var(--omo-space) * 4);
}
.create__error {
  margin: 0 0 calc(var(--omo-space) * 4);
  padding: calc(var(--omo-space) * 2.5) calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-danger);
  background: var(--omo-danger-soft);
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.create__type {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}
.create__segments {
  display: inline-grid;
  grid-auto-flow: column;
  grid-auto-columns: 1fr;
  width: max-content;
  max-width: 100%;
}
.create__segment {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: calc(var(--omo-space) * 24);
  min-height: calc(var(--omo-space) * 8);
  padding: 0 calc(var(--omo-space) * 4);
  border: var(--omo-border) solid var(--omo-line-strong);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.create__segment + .create__segment {
  margin-left: calc(var(--omo-border) * -1);
}
.create__segment:has(input:checked) {
  position: relative;
  border-color: var(--omo-accent);
  background: var(--omo-accent);
  color: var(--omo-accent-fg);
}
.create__segment:has(input:focus-visible) {
  position: relative;
  outline: var(--omo-focus-width) solid var(--omo-accent);
  outline-offset: var(--omo-focus-offset);
}
.create__more > summary {
  width: max-content;
  padding: var(--omo-space) 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.create__more > summary:focus-visible {
  outline: var(--omo-focus-width) solid var(--omo-accent);
  outline-offset: var(--omo-focus-offset);
}
.create__more-body {
  display: grid;
  gap: calc(var(--omo-space) * 4);
  padding-top: calc(var(--omo-space) * 3);
}
.create__another {
  display: inline-flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  margin-right: auto;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  cursor: pointer;
}
.create__discard {
  margin: 0 auto 0 0;
  align-self: center;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
