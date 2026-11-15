<script setup lang="ts">
// The board with static sample data, for judging the look. Served at
// /__sample by the development server only. Query parameters:
//   ?empty=1         the empty board with the first steps
//   ?drag=planning   #30 dragged out of Planning: Ready accepts, the rest refuse
//   ?theme=light|dark   force a theme
//   ?font=system     the fallback font stack of the token instead of the bundled font
//   ?sidebar=open    the sidebar expanded (collapsed by default); below 768 px a full-screen sheet
//   &tab=ceo|agents|terminals   the sidebar's tab (agents by default)
//   ?panel=13        ticket #13's panel open over the board
//   &tab=comments|diff|agents   the panel's tab (comments by default)
//   ?bell=open       the notifications open
//   ?column=review   the column shown first on narrow screens
import { onBeforeUnmount, ref } from 'vue'
import { COLUMNS, type Column } from '../api/types'
import UiDialog from '../components/ui/UiDialog.vue'
import UiTabs from '../components/ui/UiTabs.vue'
import BoardView from '../features/board/BoardView.vue'
import FirstStepsPanel, { type FirstStep } from '../features/onboarding/FirstStepsPanel.vue'
import TopBar from '../features/topbar/TopBar.vue'
import AppShell from '../layout/AppShell.vue'
import { provideBoard } from '../store'
import { applyTheme } from '../theme'
import SampleBell from './SampleBell.vue'
import SamplePanelTabs from './SamplePanelTabs.vue'
import SampleSidebar from './SampleSidebar.vue'
import SampleSidebarPanel from './SampleSidebarPanel.vue'
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

// Below 768 px the sidebar is a full-screen sheet instead of a column.
const narrowQuery = window.matchMedia('(max-width: 767.98px)')
const narrow = ref(narrowQuery.matches)
const onNarrow = (e: MediaQueryListEvent) => (narrow.value = e.matches)
narrowQuery.addEventListener('change', onNarrow)
onBeforeUnmount(() => narrowQuery.removeEventListener('change', onNarrow))
const sheetTabs = [
  { id: 'ceo', label: 'CEO', hint: 'Starts the CEO when it is not running' },
  { id: 'agents', label: 'Agents', count: sampleAgents.length },
  { id: 'terminals', label: 'Terminals', count: 3 },
]

const columns = sampleColumns({ empty, drag })

const steps: FirstStep[] = [
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
  <AppShell>
    <template #topbar>
      <TopBar>
        <template #notifications>
          <SampleBell v-model:open="bellOpen" />
        </template>
      </TopBar>
    </template>
    <template #sidebar>
      <SampleSidebar v-model:open="sidebarOpen" v-model:tab="sidebarTab" />
    </template>
    <h1 class="sr-only">Board</h1>
    <BoardView :columns="columns" :dragging-ticket="drag ? SAMPLE_DRAGGED : null" :initial-column="initialColumn">
      <template v-if="empty" #empty>
        <FirstStepsPanel :steps="steps" />
      </template>
    </BoardView>
    <SamplePanelTabs v-if="panelNumber !== null" v-model:tab="panelTab" :number="panelNumber" @close="panelNumber = null" />
    <UiDialog :open="narrow && sidebarOpen" title="Sidebar" @close="sidebarOpen = false">
      <UiTabs v-model="sidebarTab" class="sample-sheet" :tabs="sheetTabs" label="Sidebar">
        <template #panel="{ active }">
          <SampleSidebarPanel :tab="active" />
        </template>
      </UiTabs>
    </UiDialog>
  </AppShell>
</template>

<style scoped>
.sample-sheet :deep([role='tabpanel']) > * {
  padding-right: 0;
  padding-left: 0;
}
</style>
