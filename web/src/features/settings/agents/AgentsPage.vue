<script setup lang="ts">
// The Agents section of the settings.
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'

const def = sectionDef('agents')
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" />
</template>
