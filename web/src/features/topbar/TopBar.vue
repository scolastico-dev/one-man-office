<script setup lang="ts">
// The top bar: the name and connection state, the filters, and the controls
// (pause, stop all, create ticket, notifications, settings).
import Logo from '../../components/ui/Logo.vue'
import { useBoard } from '../../store'
import ConnectionStatus from './ConnectionStatus.vue'
import FilterBar from './FilterBar.vue'
import NewTicketButton from './NewTicketButton.vue'
import PauseSwitch from './PauseSwitch.vue'
import SettingsMenu from './SettingsMenu.vue'
import StopAllButton from './StopAllButton.vue'

const store = useBoard()
</script>

<template>
  <div class="topbar-frame">
    <div class="topbar">
      <div class="topbar__brand">
        <Logo :size="28" />
        <span class="topbar__name">omo</span>
        <ConnectionStatus class="topbar__connection" />
      </div>

      <FilterBar class="topbar__filters" />

      <div class="topbar__actions">
        <PauseSwitch class="topbar__pause" />
        <StopAllButton />
        <NewTicketButton />
        <slot name="notifications" />
        <SettingsMenu />
      </div>
    </div>
    <p v-if="store.state.paused" class="topbar__paused" data-paused-banner>Paused: omo starts no new agents.</p>
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
.topbar__filters {
  grid-area: filters;
}
.topbar__connection {
  margin-left: var(--omo-space);
}
.topbar__name {
  font-size: var(--omo-text-lg);
  font-weight: 700;
  line-height: var(--omo-text-lg-lh);
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
.topbar__paused {
  margin: 0;
  padding: var(--omo-space) calc(var(--omo-space) * 4);
  border-bottom: var(--omo-border) solid var(--omo-line);
  border-left: calc(var(--omo-border) * 3) solid var(--omo-warn);
  background: var(--omo-accent-soft);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
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

/* Phones: buttons keep only their icon. */
@container topbar (max-width: 767.98px) {
  .topbar {
    padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
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
