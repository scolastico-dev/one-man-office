<script setup lang="ts">
// Shown over the empty columns when filters hide every ticket.
import Panel from '../../components/ui/Panel.vue'
import UiButton from '../../components/ui/UiButton.vue'
import { useFilters } from './useFilters'

const { clear, searching } = useFilters()
</script>

<template>
  <Panel label="Nothing matches" data-nothing-matches>
    <p class="nothing__text">
      {{ searching ? 'No ticket matches these filters so far. The search is still running.' : 'No ticket matches these filters.' }}
    </p>
    <UiButton variant="primary" data-primary @click="clear()">Clear filters</UiButton>
  </Panel>
</template>

<style scoped>
.nothing__text {
  margin: 0 0 calc(var(--omo-space) * 3);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
</style>
