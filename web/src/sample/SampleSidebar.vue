<script setup lang="ts">
// Where the sidebar goes and how much room it takes, collapsed and expanded.
// Its tabs are placeholders and do nothing.
import IconButton from '../components/ui/IconButton.vue'

defineProps<{ open: boolean }>()
const emit = defineEmits<{ toggle: [] }>()

const tabs = ['CEO', 'Agents', 'Terminals']
</script>

<template>
  <aside class="side" :class="{ 'side--open': open }" aria-label="Sidebar (placeholder)">
    <div class="side__head">
      <IconButton :label="open ? 'Collapse sidebar' : 'Expand sidebar'" size="sm" @click="emit('toggle')">
        <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
          <path v-if="open" d="M7.5 2 3.5 6l4 4M10.5 2v8" />
          <path v-else d="M4.5 2l4 4-4 4M1.5 2v8" />
        </svg>
      </IconButton>
    </div>
    <div class="side__tabs" role="tablist" aria-label="Sidebar tabs (placeholder)">
      <button
        v-for="(tab, i) in tabs"
        :key="tab"
        type="button"
        role="tab"
        class="side__tab"
        :aria-selected="open && i === 0 ? 'true' : 'false'"
        aria-disabled="true"
        title="Placeholder"
      >
        {{ tab }}
      </button>
    </div>
    <div v-if="open" class="side__body">
      <p class="side__note">
        Placeholder. The CEO terminal, the list of running agents and your own terminals take this space.
      </p>
    </div>
  </aside>
</template>

<style scoped>
.side {
  display: flex;
  flex-direction: column;
  width: calc(var(--omo-space) * 11);
  height: 100%;
  border-right: var(--omo-border) solid var(--omo-line);
  background: var(--omo-surface);
}
.side--open {
  width: calc(var(--omo-space) * 76);
}
.side__head {
  display: flex;
  justify-content: center;
  padding: calc(var(--omo-space) * 2) 0;
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.side--open .side__head {
  justify-content: flex-end;
  padding-right: calc(var(--omo-space) * 2);
}
.side__tabs {
  display: flex;
  flex-direction: column;
}
.side--open .side__tabs {
  flex-direction: row;
  border-bottom: var(--omo-border) solid var(--omo-line);
}
.side__tab {
  padding: calc(var(--omo-space) * 3) 0;
  border: 0;
  border-bottom: var(--omo-border) dashed var(--omo-line);
  background: transparent;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  writing-mode: vertical-rl;
  cursor: not-allowed;
}
.side--open .side__tab {
  flex: 1 1 0;
  padding: calc(var(--omo-space) * 2);
  border-bottom: 0;
  border-right: var(--omo-border) dashed var(--omo-line);
  writing-mode: horizontal-tb;
}
.side__tab[aria-selected='true'] {
  color: var(--omo-fg);
  box-shadow: inset 0 calc(var(--omo-border) * -2) 0 var(--omo-line-strong);
}
.side__body {
  flex: 1 1 auto;
  margin: calc(var(--omo-space) * 3);
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) dashed var(--omo-line-strong);
  background: repeating-linear-gradient(
    -45deg,
    var(--omo-raised) 0 var(--omo-border),
    transparent var(--omo-border) calc(var(--omo-space) * 2)
  );
}
.side__note {
  margin: 0;
  padding: calc(var(--omo-space) * 2);
  background: var(--omo-surface);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
@media (max-width: 767.98px) {
  .side {
    display: none;
  }
}
</style>
