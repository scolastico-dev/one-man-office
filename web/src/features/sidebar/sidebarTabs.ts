// The sidebar's tabs: which there are, when each shows, and where its content
// comes from. The CEO and Terminals tabs load when first shown.
import { defineAsyncComponent, type Component } from 'vue'
import AgentsTab from './AgentsTab.vue'
import type { Sidebar } from './useSidebar'

export interface TabContext {
  ceoEnabled: boolean
}

export interface SidebarTab {
  id: string
  label: string
  hint?: string
  visible?(ctx: TabContext): boolean
  component: Component
  /** The props the tab's component takes, from the sidebar's state. */
  props?(sidebar: Sidebar): Record<string, unknown>
}

export const sidebarTabs: SidebarTab[] = [
  {
    id: 'ceo',
    label: 'CEO',
    hint: 'Starts the CEO when it is not running',
    visible: (ctx) => ctx.ceoEnabled,
    component: defineAsyncComponent(() => import('./CeoTab.vue')),
    props: (s) => ({ how: s.how.value }),
  },
  { id: 'agents', label: 'Agents', component: AgentsTab },
  {
    id: 'terminals',
    label: 'Terminals',
    component: defineAsyncComponent(() => import('./TerminalsTab.vue')),
    props: (s) => ({ selected: s.selection.value }),
  },
]

/** The tabs that show now. */
export function visibleTabs(ctx: TabContext): SidebarTab[] {
  return sidebarTabs.filter((t) => t.visible?.(ctx) ?? true)
}
