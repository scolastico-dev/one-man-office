<script setup lang="ts">
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'

const def = sectionDef('skills')
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" />
</template>
