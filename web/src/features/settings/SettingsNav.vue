<script setup lang="ts">
// The list of settings sections. Each entry is a link to its section, so it
// can be opened in a new tab and reached with Tab; the section shown is
// marked as the current page.
import { useRoute } from 'vue-router'
import Panel from '../../components/ui/Panel.vue'
import { settingsLocation, type SettingsSection } from '../../locations'
import { SETTINGS_SECTIONS } from './settingsSections'

defineProps<{
  /** The section shown beside the list; null when none is (the list is a page of its own). */
  current: SettingsSection | null
}>()

const route = useRoute()
</script>

<template>
  <Panel as="nav" label="Settings" :padded="false" data-settings-nav>
    <ul class="settings-nav">
      <li v-for="s in SETTINGS_SECTIONS" :key="s.id">
        <RouterLink v-slot="{ href, navigate }" :to="settingsLocation(s.id, route)" custom>
          <a :href="href" class="settings-nav__link" :aria-current="s.id === current ? 'page' : undefined" :data-section="s.id" @click="navigate">
            <span class="settings-nav__label">{{ s.label }}</span>
            <span class="settings-nav__desc">{{ s.description }}</span>
          </a>
        </RouterLink>
      </li>
    </ul>
  </Panel>
</template>

<style scoped>
.settings-nav {
  margin: 0;
  padding: calc(var(--omo-space) * 4) 0 calc(var(--omo-space) * 2);
  list-style: none;
}
.settings-nav__link {
  display: grid;
  gap: calc(var(--omo-space) * 0.5);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 3);
  border-left: var(--omo-edge) solid transparent;
  color: var(--omo-fg);
  text-decoration: none;
  transition:
    background-color var(--omo-duration) ease,
    border-color var(--omo-duration) ease;
}
.settings-nav__link:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.settings-nav__link[aria-current='page'] {
  border-left-color: var(--omo-accent);
  background: var(--omo-select);
}
.settings-nav__label {
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.settings-nav__link[aria-current='page'] .settings-nav__label {
  font-weight: 700;
}
.settings-nav__desc {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
@media (hover: hover) {
  .settings-nav__link:hover:not([aria-current='page']) {
    border-left-color: var(--omo-line-strong);
    background: var(--omo-accent-soft);
  }
}
</style>
