<script setup lang="ts">
// The board with static sample data, for judging the look. Served at
// /__sample by the development server only. Query parameters:
//   ?empty=1         the empty board with the first steps
//   ?drag=planning   #30 dragged out of Planning: Ready accepts, the rest refuse
//   ?theme=light|dark   force a theme
//   ?font=system     the fallback font stack of the token instead of the bundled font
//   ?sidebar=open    the sidebar expanded (collapsed by default)
//   ?column=review   the column shown first on narrow screens
import { ref } from 'vue'
import { COLUMNS, type Column } from '../api/types'
import IconButton from '../components/ui/IconButton.vue'
import BoardView from '../features/board/BoardView.vue'
import FirstStepsPanel, { type FirstStep } from '../features/onboarding/FirstStepsPanel.vue'
import TopBar from '../features/topbar/TopBar.vue'
import AppShell from '../layout/AppShell.vue'
import { provideBoard } from '../store'
import { applyTheme } from '../theme'
import SampleSidebar from './SampleSidebar.vue'
import { SAMPLE_DRAGGED, sampleColumns } from './sampleBoard'
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
          <IconButton label="Notifications (placeholder)" class="sample-bell">
            <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
              <path d="M3.5 11.5V7a4.5 4.5 0 0 1 9 0v4.5l1 1.5h-11zM6.5 14.2h3" />
            </svg>
          </IconButton>
        </template>
      </TopBar>
    </template>
    <template #sidebar>
      <SampleSidebar :open="sidebarOpen" @toggle="sidebarOpen = !sidebarOpen" />
    </template>
    <h1 class="sr-only">Board</h1>
    <BoardView :columns="columns" :dragging-ticket="drag ? SAMPLE_DRAGGED : null" :initial-column="initialColumn">
      <template v-if="empty" #empty>
        <FirstStepsPanel :steps="steps" />
      </template>
    </BoardView>
  </AppShell>
</template>

<style scoped>
.sample-bell {
  border-style: dashed;
  border-color: var(--omo-line-strong);
}
</style>
