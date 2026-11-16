<script setup lang="ts">
// The Diff tab: in the panel the files stacked (DiffView), on the ticket's
// page the file browser with the folder tree (DiffBrowser). Both are loaded
// when first shown, so the diff code stays out of the entry chunk.
import type { TicketSummary } from '../../../api/types'
import { lazyComponent } from '../../../components/ui/chunkFailure'
import { useTicketLayout } from '../ticketLayout'

defineProps<{ ticket: TicketSummary }>()

const DiffView = lazyComponent(() => import('../../diff/DiffView.vue'))
const DiffBrowser = lazyComponent(() => import('../../diff/DiffBrowser.vue'))
const layout = useTicketLayout()
</script>

<template>
  <h3 class="sr-only">Diff</h3>
  <component :is="DiffBrowser" v-if="layout === 'page'" :ticket="ticket" />
  <component :is="DiffView" v-else :ticket="ticket" />
</template>
