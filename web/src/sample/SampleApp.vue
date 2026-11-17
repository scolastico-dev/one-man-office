<script setup lang="ts">
// The board with static sample data, for judging the look. Served at
// /__sample by the development server only. Query parameters:
//   ?empty=1         the empty board with the first steps
//   ?drag=planning   #30 dragged out of Planning: Ready accepts, the rest refuse
//   ?theme=light|dark   force a theme
//   ?font=system     the fallback font stack of the token instead of the bundled font
//   ?sidebar=open    the sidebar expanded (collapsed by default); below 768 px a full-screen sheet
//   &tab=ceo|agents|terminals   the sidebar's tab (agents by default)
//   &usage=1         the Agents tab starts with the usage of each account; with
//                    ?panel the action row has Start anyway… and a plugin's action
//   ?panel=13        ticket #13's panel open over the board
//   &tab=comments|diff|agents   the panel's tab (comments by default)
//   ?bell=open       the notifications open
//   ?column=review   the column shown first on narrow screens
//   ?settings=projects|agents|roles|usage|prompts|skills|environment|limits|review|plugins|general
//                    the settings in place of the board; `list` shows the section list
//                    (below 768 px a page of its own)
//   &picker=open     the folder picker open over the settings
//   &test=error      on Usage, the open script's test failed
import { onBeforeUnmount, ref } from 'vue'
import { COLUMNS, type Column } from '../api/types'
import UiDialog from '../components/ui/UiDialog.vue'
import UiTabs from '../components/ui/UiTabs.vue'
import BoardView from '../features/board/BoardView.vue'
import FirstStepsPanel, { type FirstStep } from '../features/onboarding/FirstStepsPanel.vue'
import { registerTicketAction } from '../features/ticket/actions'
import TopBar from '../features/topbar/TopBar.vue'
import AppShell from '../layout/AppShell.vue'
import { provideBoard } from '../store'
import { applyTheme } from '../theme'
import SampleBell from './SampleBell.vue'
import SamplePanelTabs from './SamplePanelTabs.vue'
import SampleSidebar from './SampleSidebar.vue'
import SampleSidebarPanel from './SampleSidebarPanel.vue'
import SampleSettings from './SampleSettings.vue'
import { SAMPLE_SECTIONS, type SampleSection } from './sampleSettings'
import { SAMPLE_DRAGGED, sampleAgents, sampleColumns } from './sampleBoard'
import { createSampleStore } from './sampleStore'

// The top bar reads the store; give it one over the sample tickets.
provideBoard(createSampleStore())

const params = new URLSearchParams(window.location.search)
const empty = params.get('empty') === '1'
const drag = !empty && params.get('drag') === 'planning'
const column = params.get('column') as Column | null
const initialColumn: Column = column && COLUMNS.includes(column) ? column : drag ? 'ready' : 'planning'

const theme = params.get('theme')
if (theme === 'light' || theme === 'dark') applyTheme(theme, document.documentElement)

if (params.get('font') === 'system') {
  // Drop the bundled family, the first entry of the token, and keep the fallbacks.
  const root = document.documentElement
  const families = getComputedStyle(root).getPropertyValue('--omo-font').split(',')
  if (families.length > 1) root.style.setProperty('--omo-font', families.slice(1).join(',').trim())
}

const sidebarOpen = ref(params.get('sidebar') === 'open')
const panelNumber = ref(Number(params.get('panel')) || null)
const tabParam = params.get('tab') ?? ''
const sidebarTab = ref(panelNumber.value === null && ['ceo', 'agents', 'terminals'].includes(tabParam) ? tabParam : 'agents')
const panelTab = ref(panelNumber.value !== null && ['comments', 'diff', 'agents'].includes(tabParam) ? tabParam : 'comments')
const bellOpen = ref(params.get('bell') === 'open')
const settingsParam = params.get('settings')
const settings = settingsParam !== null
const settingsSection = SAMPLE_SECTIONS.some((s) => s.id === settingsParam) ? (settingsParam as SampleSection) : null
const pickerOpen = settings && params.get('picker') === 'open'
const testFailed = params.get('test') === 'error'
const usage = params.get('usage') === '1'

// The actions later features add to the panel: Start anyway for a ticket
// that waits for usage on its own model, and a plugin's ticket action.
if (usage) {
  const removers = [
    registerTicketAction({
      id: 'start-anyway',
      label: 'Start anyway…',
      applies: () => true,
      describe: () => 'Starts it on claude/opus although the model is near its usage limit.',
      run: () => {},
    }),
    registerTicketAction({
      id: 'plugin:pushover:send',
      label: 'pushover: Send this ticket to my phone',
      applies: () => true,
      describe: () => 'Runs the action of the plugin pushover for this ticket.',
      run: () => {},
    }),
  ]
  onBeforeUnmount(() => removers.forEach((r) => r()))
}

// Below 768 px the sidebar is a full-screen sheet instead of a column.
const narrowQuery = window.matchMedia('(max-width: 767.98px)')
const narrow = ref(narrowQuery.matches)
const onNarrow = (e: MediaQueryListEvent) => (narrow.value = e.matches)
narrowQuery.addEventListener('change', onNarrow)
onBeforeUnmount(() => narrowQuery.removeEventListener('change', onNarrow))
const sheetTabs = [
  { id: 'ceo', label: 'CEO', hint: 'Starts the CEO unless you stopped it; a stopped CEO starts with its Start button' },
  { id: 'agents', label: 'Agents', count: sampleAgents.length },
  { id: 'terminals', label: 'Terminals', count: 3 },
]

const columns = sampleColumns({ empty, drag })

const steps: FirstStep[] = [
  {
    id: 'superpowers',
    title: 'Add the Superpowers skill set (recommended)',
    detail: 'A public set of skills (brainstorming, test-driven development, code review) that omo fetches from GitHub and tells your agents to follow.',
    done: false,
    action: 'Add Superpowers',
    secondary: 'Skill sets page',
    optional: true,
  },
  {
    id: 'project',
    title: 'Add a project',
    detail: 'A folder with a git repository that agents may work in. 1 project added.',
    done: true,
    action: 'Projects',
  },
  {
    id: 'agents',
    title: 'Check the agent setup',
    detail: 'Product manager, developer and reviewer each need at least one usable model.',
    done: false,
    action: 'Agent setup',
  },
  {
    id: 'ticket',
    title: 'Create a first ticket',
    detail: 'A story for a goal with several tasks, or a single task. It starts in Planning.',
    done: false,
    action: 'Create ticket',
  },
]
</script>

<template>
  <AppShell :class="{ 'sample-settings': settings }">
    <template #topbar>
      <TopBar>
        <template #notifications>
          <SampleBell v-model:open="bellOpen" />
        </template>
      </TopBar>
    </template>
    <template #sidebar>
      <SampleSidebar v-model:open="sidebarOpen" v-model:tab="sidebarTab" :usage="usage" />
    </template>
    <SampleSettings v-if="settings" :section="settingsSection" :picker="pickerOpen" :test-failed="testFailed" />
    <h1 v-if="!settings" class="sr-only">Board</h1>
    <BoardView v-if="!settings" :columns="columns" :dragging-ticket="drag ? SAMPLE_DRAGGED : null" :initial-column="initialColumn">
      <template v-if="empty" #empty>
        <FirstStepsPanel :steps="steps" />
      </template>
    </BoardView>
    <SamplePanelTabs v-if="panelNumber !== null" v-model:tab="panelTab" :number="panelNumber" @close="panelNumber = null" />
    <UiDialog class="side-sheet" :open="narrow && sidebarOpen" title="Sidebar" @close="sidebarOpen = false">
      <UiTabs v-model="sidebarTab" class="sample-sheet side-sheet__tabs" :tabs="sheetTabs" label="Sidebar" fill>
        <template #panel="{ active }">
          <SampleSidebarPanel :tab="active" :usage="usage" />
        </template>
      </UiTabs>
    </UiDialog>
  </AppShell>
</template>

<style scoped>
/* On the settings the top bar shows no filters. */
.sample-settings :deep(.topbar__filters) {
  display: none;
}
.sample-sheet :deep([role='tabpanel']) > * {
  padding-right: 0;
  padding-left: 0;
}
</style>

<style>
/* As the real sheet (SidebarRoot): the tabs take the sheet's height. */
.side-sheet[open] > .dialog__body {
  display: flex;
  flex-direction: column;
}
.side-sheet__tabs {
  flex: 1 1 auto;
}
</style>
