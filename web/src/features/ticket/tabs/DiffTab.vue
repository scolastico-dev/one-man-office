<script setup lang="ts">
// The Diff tab: in the panel the files stacked (DiffView), on the ticket's
// page the file browser with the folder tree (DiffBrowser, loaded when first
// shown).
import type { TicketSummary } from '../../../api/types'
import { lazyComponent } from '../../../components/ui/chunkFailure'
import DiffView from '../../diff/DiffView.vue'
import { useTicketLayout } from '../ticketLayout'

defineProps<{ ticket: TicketSummary }>()

const DiffBrowser = lazyComponent(() => import('../../diff/DiffBrowser.vue'))
const layout = useTicketLayout()
</script>

<template>
  <h3 class="sr-only">Diff</h3>
  <component :is="DiffBrowser" v-if="layout === 'page'" :ticket="ticket" />
  <DiffView v-else :ticket="ticket" />
</template>
