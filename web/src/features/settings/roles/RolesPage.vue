<script setup lang="ts">
// The Roles section of the settings: the agent setup as it is.
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'
import SetupReport from './SetupReport.vue'

const def = sectionDef('roles')
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description">
    <SetupReport />
  </SettingsPage>
</template>
