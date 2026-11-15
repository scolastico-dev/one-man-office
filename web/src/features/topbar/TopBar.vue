<script setup lang="ts">
import { ref } from 'vue'
import Logo from '../../components/ui/Logo.vue'
import Toggle from '../../components/ui/Toggle.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiMenu, { type MenuItem } from '../../components/ui/UiMenu.vue'

const props = withDefaults(defineProps<{ paused?: boolean }>(), { paused: false })

// Static look: the controls keep local state only.
const pausedState = ref(props.paused)
const needsMe = ref(false)

const settingsItems: MenuItem[] = [
  { id: 'projects', label: 'Projects' },
  { id: 'agents', label: 'Agent setup' },
  { id: 'theme', label: 'Theme' },
]
</script>

<template>
  <div class="topbar-frame">
    <div class="topbar">
      <div class="topbar__brand">
        <Logo :size="28" />
        <span class="topbar__name">omo</span>
      </div>

      <form class="topbar__filters" role="search" aria-label="Filter the board" @submit.prevent>
        <label class="topbar__wide">
          <span class="sr-only">Project</span>
          <select class="omo-input topbar__control">
            <option>All projects</option>
          </select>
        </label>
        <label class="topbar__wide">
          <span class="sr-only">Story</span>
          <select class="omo-input topbar__control">
            <option>All stories</option>
          </select>
        </label>
        <label class="topbar__wide">
          <span class="sr-only">Supervisor</span>
          <select class="omo-input topbar__control">
            <option>Any supervisor</option>
          </select>
        </label>
        <button type="button" class="omo-btn topbar__wide" :aria-pressed="needsMe" @click="needsMe = !needsMe">
          Needs me
        </button>
        <button type="button" class="omo-btn topbar__narrow" aria-haspopup="dialog">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
            <path d="M.8 1.5h10.4L7.2 6.4v4.1L4.8 9.4v-3z" />
          </svg>
          Filters
        </button>
        <label class="topbar__search">
          <span class="sr-only">Search tickets</span>
          <input class="omo-input topbar__control" type="search" placeholder="Search #12 or title" />
          <kbd class="topbar__kbd" aria-hidden="true">/</kbd>
        </label>
      </form>

      <div class="topbar__actions">
        <Toggle v-model="pausedState" class="topbar__pause" label="Pause" />
        <UiButton variant="danger" aria-label="Stop all agents" title="Stop all agents">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
            <rect x="1.5" y="1.5" width="9" height="9" fill="currentColor" />
          </svg>
          <span class="topbar__label">Stop all</span>
        </UiButton>
        <UiButton variant="primary" aria-label="Create ticket" title="Create ticket (c)">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.6">
            <path d="M6 1v10M1 6h10" />
          </svg>
          <span class="topbar__label">Create ticket</span>
        </UiButton>
        <slot name="notifications" />
        <UiMenu label="Settings" :items="settingsItems" icon-only variant="ghost" align="end">
          <template #icon>
            <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
              <path
                d="M13.24 6.68 15.09 6.73 15.09 9.27 13.24 9.32 12.99 10.07 12.64 10.77 13.91 12.12 12.12 13.91 10.77 12.64 10.07 12.99 9.32 13.24 9.27 15.09 6.73 15.09 6.68 13.24 5.93 12.99 5.23 12.64 3.88 13.91 2.09 12.12 3.36 10.77 3.01 10.07 2.76 9.32 .91 9.27 .91 6.73 2.76 6.68 3.01 5.93 3.36 5.23 2.09 3.88 3.88 2.09 5.23 3.36 5.93 3.01 6.68 2.76 6.73 .91 9.27 .91 9.32 2.76 10.07 3.01 10.77 3.36 12.12 2.09 13.91 3.88 12.64 5.23 12.99 5.93Z"
                stroke-linejoin="round"
              />
              <circle cx="8" cy="8" r="2.3" />
            </svg>
          </template>
        </UiMenu>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* The layout follows the room the bar has, which shrinks when a sidebar is open. */
.topbar-frame {
  container: topbar / inline-size;
}
.topbar {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  grid-template-areas: 'brand filters actions';
  align-items: center;
  gap: calc(var(--omo-space) * 2) calc(var(--omo-space) * 4);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 4);
  border-bottom: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.topbar__brand {
  grid-area: brand;
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.topbar__name {
  font-size: var(--omo-text-lg);
  font-weight: 700;
  line-height: var(--omo-text-lg-lh);
}
.topbar__filters {
  grid-area: filters;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  margin: 0;
}
.topbar__control {
  min-height: calc(var(--omo-space) * 8);
  padding: calc(var(--omo-space) * 1) calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
select.topbar__control {
  width: auto;
  max-width: calc(var(--omo-space) * 40);
}
.topbar__search {
  position: relative;
  display: flex;
  flex: 1 1 calc(var(--omo-space) * 40);
  min-width: calc(var(--omo-space) * 30);
  max-width: calc(var(--omo-space) * 80);
}
.topbar__search .topbar__control {
  padding-right: calc(var(--omo-space) * 8);
}
.topbar__kbd {
  position: absolute;
  top: 50%;
  right: calc(var(--omo-space) * 2);
  padding: 0 var(--omo-space);
  border: var(--omo-border) solid var(--omo-line-strong);
  color: var(--omo-muted);
  font-family: inherit;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  transform: translateY(-50%);
}
.topbar__narrow {
  display: none;
}
.topbar__actions {
  grid-area: actions;
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
}
.topbar__pause {
  margin-right: calc(var(--omo-space) * 2);
}

/* Tablets, half-width windows, an open sidebar: filters get their own row. */
@container topbar (max-width: 1299.98px) {
  .topbar {
    grid-template-columns: auto minmax(0, 1fr);
    grid-template-areas:
      'brand actions'
      'filters filters';
  }
  .topbar__actions {
    justify-content: flex-end;
  }
}

/* Phones: filters move into a sheet, buttons keep only their icon. */
@container topbar (max-width: 767.98px) {
  .topbar {
    padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  }
  .topbar__wide {
    display: none;
  }
  .topbar__narrow {
    display: inline-flex;
  }
  .topbar__search {
    min-width: 0;
    max-width: none;
  }
  .topbar__filters {
    flex-wrap: nowrap;
  }
  .topbar__label {
    display: none;
  }
  .topbar__actions {
    gap: var(--omo-space);
  }
  .topbar__actions :deep(.omo-btn:not(.omo-btn--icon)) {
    width: calc(var(--omo-space) * 8);
    padding: 0;
  }
  .topbar__pause {
    margin-right: var(--omo-space);
  }
}
</style>
