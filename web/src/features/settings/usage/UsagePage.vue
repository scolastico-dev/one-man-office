<script setup lang="ts">
import SettingsPage from '../SettingsPage.vue'
import { sectionDef } from '../settingsSections'

const def = sectionDef('usage')
</script>

<template>
  <SettingsPage :title="def.label" :description="def.description" />
</template>
