<script setup lang="ts">
// The board filters in the top bar: project, story, supervisor, "Needs me"
// with its count, and search (`/` focuses it); the active story as a
// removable chip; below 768 px a "Filters" button opening a sheet with the
// same controls. The values live in `useFilters`.
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Chip from '../../components/ui/Chip.vue'
import { BOARD_SUSPENDED } from '../../components/ui/dialogHost'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { useBoard } from '../../store'
import FilterFields from '../filters/FilterFields.vue'
import { useFilters } from '../filters/useFilters'

const store = useBoard()
/** The board waits under the lock screen: its keys do nothing. */
const suspended = inject(BOARD_SUSPENDED, null)
const { filters, searching, searchFailed, activeCount, clear } = useFilters()

const text = ref(filters.value.q ?? '')
watch(
  () => filters.value.q,
  (q) => {
    if ((q ?? '') !== text.value) text.value = q ?? ''
  },
)
function onSearch() {
  const f = { ...filters.value }
  if (text.value.trim() === '') delete f.q
  else f.q = text.value
  filters.value = f
}

const story = computed(() => {
  const n = filters.value.story
  return n === undefined ? null : { number: n, title: store.view(n)?.title ?? '' }
})
function removeStory() {
  const f = { ...filters.value }
  delete f.story
  filters.value = f
}

const search = ref<HTMLInputElement>()
const sheet = ref(false)

/** `/` jumps to the search field unless the user is typing somewhere or a dialog is open. */
function onKey(e: KeyboardEvent) {
  if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return
  const el = document.activeElement
  if (el instanceof HTMLElement && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return
  if (suspended?.value || document.querySelector('dialog[open]')) return
  e.preventDefault()
  search.value?.focus()
  search.value?.select()
}
onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))

function onSearchKey(e: KeyboardEvent) {
  if (e.key !== 'Escape') return
  if (text.value !== '') {
    e.preventDefault()
    text.value = ''
    onSearch()
  } else search.value?.blur()
}
</script>

<template>
  <form class="topbar__filters" role="search" aria-label="Filter the board" @submit.prevent>
    <FilterFields class="topbar__wide" />
    <UiButton class="topbar__narrow" aria-haspopup="dialog" @click="sheet = true">
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
        <path d="M.8 1.5h10.4L7.2 6.4v4.1L4.8 9.4v-3z" />
      </svg>
      {{ activeCount > 0 ? `Filters (${activeCount})` : 'Filters' }}
    </UiButton>
    <label class="topbar__search">
      <span class="sr-only">Search tickets</span>
      <input
        ref="search"
        v-model="text"
        class="omo-input omo-input--sm topbar__control"
        type="search"
        placeholder="Search #12, title, description"
        aria-keyshortcuts="/"
        @input="onSearch"
        @keydown="onSearchKey"
      />
      <kbd class="topbar__kbd" aria-hidden="true">/</kbd>
    </label>
    <Chip
      v-if="story"
      as="button"
      class="topbar__chip"
      :label="`Story #${story.number}${story.title ? ' ' + story.title : ''} ×`"
      :aria-label="`Remove the story filter: #${story.number} ${story.title}`"
      title="Remove the story filter"
      data-story-chip
      @click="removeStory"
    />
    <UiButton v-if="activeCount > 0" class="topbar__wide" size="sm" data-clear-filters @click="clear()">Clear filters</UiButton>
    <p class="topbar__status" role="status" data-search-status>
      <template v-if="searching">Searching…</template>
      <template v-else-if="searchFailed">The server could not search descriptions; showing matches in numbers and titles only.</template>
    </p>
    <UiDialog :open="sheet" title="Filters" @close="sheet = false">
      <FilterFields layout="sheet" />
      <template #actions>
        <UiButton :disabled="activeCount === 0" @click="clear()">Clear filters</UiButton>
        <UiButton variant="primary" @click="sheet = false">Done</UiButton>
      </template>
    </UiDialog>
  </form>
</template>

<style scoped>
.topbar__filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  margin: 0;
}
.topbar__search {
  position: relative;
  display: flex;
  flex: 1 1 calc(var(--omo-space) * 40);
  min-width: calc(var(--omo-space) * 30);
  max-width: calc(var(--omo-space) * 80);
}
.topbar__search .topbar__control {
  padding-right: calc(var(--omo-space) * 8);
}
.topbar__kbd {
  position: absolute;
  top: 50%;
  right: calc(var(--omo-space) * 2);
  padding: 0 var(--omo-space);
  border: var(--omo-border) solid var(--omo-line-strong);
  color: var(--omo-muted);
  font-family: inherit;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  transform: translateY(-50%);
}
.topbar__narrow {
  display: none;
}
.topbar__status {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.topbar__status:empty {
  display: none;
}

/* Phones: filters move into a sheet. */
@container topbar (max-width: 767.98px) {
  .topbar__filters .topbar__wide {
    display: none;
  }
  .topbar__narrow {
    display: inline-flex;
  }
  .topbar__search {
    min-width: 0;
    max-width: none;
  }
  .topbar__filters {
    flex-wrap: nowrap;
  }
}
</style>
