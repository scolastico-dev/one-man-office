<script setup lang="ts">
// The Prompts section of the settings.
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'

const def = sectionDef('prompts')
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" />
</template>
