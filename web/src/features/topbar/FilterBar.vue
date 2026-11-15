<script setup lang="ts">
// The board filters in the top bar: project, story, supervisor, "Needs me"
// with its count, and search (`/` focuses it); the active story as a
// removable chip; below 768 px a "Filters" button opening a sheet with the
// same controls. The values live in `useFilters`.
//
// For now the controls show their final look and change nothing.
import { ref } from 'vue'

const needsMe = ref(false)
</script>

<template>
  <form class="topbar__filters" role="search" aria-label="Filter the board" @submit.prevent>
    <label class="topbar__wide">
      <span class="sr-only">Project</span>
      <select class="omo-input topbar__control">
        <option>All projects</option>
      </select>
    </label>
    <label class="topbar__wide">
      <span class="sr-only">Story</span>
      <select class="omo-input topbar__control">
        <option>All stories</option>
      </select>
    </label>
    <label class="topbar__wide">
      <span class="sr-only">Supervisor</span>
      <select class="omo-input topbar__control">
        <option>Any supervisor</option>
      </select>
    </label>
    <button type="button" class="omo-btn topbar__wide" :aria-pressed="needsMe" @click="needsMe = !needsMe">
      Needs me
    </button>
    <button type="button" class="omo-btn topbar__narrow" aria-haspopup="dialog">
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
        <path d="M.8 1.5h10.4L7.2 6.4v4.1L4.8 9.4v-3z" />
      </svg>
      Filters
    </button>
    <label class="topbar__search">
      <span class="sr-only">Search tickets</span>
      <input class="omo-input topbar__control" type="search" placeholder="Search #12 or title" />
      <kbd class="topbar__kbd" aria-hidden="true">/</kbd>
    </label>
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
.topbar__control {
  min-height: calc(var(--omo-space) * 8);
  padding: calc(var(--omo-space) * 1) calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
select.topbar__control {
  width: auto;
  max-width: calc(var(--omo-space) * 40);
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

/* Phones: filters move into a sheet. */
@container topbar (max-width: 767.98px) {
  .topbar__wide {
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
