<script setup lang="ts">
// The Review section of the settings.
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'

const def = sectionDef('review')
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" />
</template>
