<script setup lang="ts">
// The notification bell, open or closed, with static data: what waits for
// the user, and comments that mention the user.
import { ref } from 'vue'
import IconButton from '../components/ui/IconButton.vue'
import RelativeTime from '../components/ui/RelativeTime.vue'
import StatusBadge from '../components/ui/StatusBadge.vue'
import Toggle from '../components/ui/Toggle.vue'
import { sampleNotifications } from './sampleBoard'

const open = defineModel<boolean>('open', { required: true })

const { waiting, mentions } = sampleNotifications
const unread = mentions.filter((m) => m.unread).length
const label = `Notifications: ${waiting.length} waiting for you, ${unread} unread mention${unread === 1 ? '' : 's'}`
const desktop = ref(false)
</script>

<template>
  <div class="bell">
    <IconButton :label="label" :aria-expanded="open ? 'true' : 'false'" aria-controls="sample-bell-list" @click="open = !open">
      <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.4">
        <path d="M3.5 11.5V7a4.5 4.5 0 0 1 9 0v4.5l1 1.5h-11zM6.5 14.2h3" />
      </svg>
    </IconButton>
    <span class="bell__count" aria-hidden="true">{{ waiting.length + unread }}</span>
    <div v-if="open" id="sample-bell-list" class="bell__list" role="region" aria-label="Notifications">
      <section class="bell__group" aria-labelledby="sample-bell-waiting">
        <h2 id="sample-bell-waiting" class="bell__h">Waiting for you <span class="bell__n">{{ waiting.length }}</span></h2>
        <ul class="bell__items">
          <li v-for="w in waiting" :key="w.ticket">
            <a class="bell__item" :href="`/${w.ticket}`" @click.prevent>
              <span class="bell__title"><span class="bell__num">#{{ w.ticket }}</span> {{ w.title }}</span>
              <StatusBadge :tone="w.tone" :text="w.why" />
            </a>
          </li>
        </ul>
      </section>
      <section class="bell__group" aria-labelledby="sample-bell-mentions">
        <h2 id="sample-bell-mentions" class="bell__h">Mentions <span class="bell__n">{{ unread }} unread</span></h2>
        <ul class="bell__items">
          <li v-for="m in mentions" :key="m.id">
            <a class="bell__item" :class="{ 'bell__item--unread': m.unread }" :href="`/${m.ticket}?tab=comments&comment=${m.id}`" @click.prevent>
              <span class="bell__meta"
                ><span v-if="m.unread" class="bell__dot" aria-hidden="true"></span><span v-if="m.unread" class="sr-only">Unread: </span
                ><span class="bell__num">#{{ m.ticket }}</span> {{ m.by }} · <RelativeTime :at="m.at"
              /></span>
              <span class="bell__text">{{ m.text }}</span>
            </a>
          </li>
        </ul>
      </section>
      <div class="bell__foot">
        <Toggle v-model="desktop" label="Desktop notifications" />
        <button type="button" class="bell__mark">Mark all as read</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.bell {
  position: relative;
}
.bell__count {
  position: absolute;
  top: calc(var(--omo-space) * -1.5);
  right: calc(var(--omo-space) * -1.5);
  min-width: calc(var(--omo-space) * 4);
  padding: 0 var(--omo-space);
  background: var(--omo-warn);
  color: var(--omo-bg);
  font-size: var(--omo-text-xs);
  font-weight: 700;
  line-height: var(--omo-text-xs-lh);
  text-align: center;
  pointer-events: none;
}
.bell__list {
  position: absolute;
  z-index: 30;
  top: calc(100% + var(--omo-space) * 1.5);
  right: 0;
  display: grid;
  gap: calc(var(--omo-space) * 3);
  width: 380px;
  max-width: calc(100vw - var(--omo-space) * 6);
  max-height: calc(100dvh - var(--omo-space) * 20);
  overflow-y: auto;
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-accent);
  background: var(--omo-surface);
  box-shadow: var(--omo-space) var(--omo-space) 0 var(--omo-backdrop);
}
/* Phones: the list spans the screen under the top bar instead of hanging from the bell. */
@media (max-width: 767.98px) {
  .bell__list {
    position: fixed;
    top: calc(var(--omo-space) * 14);
    right: calc(var(--omo-space) * 3);
    left: calc(var(--omo-space) * 3);
    width: auto;
    max-width: none;
  }
}
.bell__group {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
}
.bell__h {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.bell__n {
  color: var(--omo-accent);
  letter-spacing: 0;
  text-transform: none;
}
.bell__items {
  display: grid;
  margin: 0;
  padding: 0;
  list-style: none;
}
.bell__item {
  display: grid;
  justify-items: start;
  gap: var(--omo-space);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  border: var(--omo-border) solid transparent;
  border-left: var(--omo-edge) solid transparent;
  color: var(--omo-fg);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  text-decoration: none;
}
.bell__item:focus-visible {
  border-color: var(--omo-select-line);
  background: var(--omo-select);
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.bell__item--unread {
  border-left-color: var(--omo-accent);
}
@media (hover: hover) {
  .bell__item:hover {
    background: var(--omo-accent-soft);
  }
}
.bell__title,
.bell__text {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bell__num {
  color: var(--omo-muted);
}
.bell__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0 var(--omo-space);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.bell__item--unread .bell__text {
  font-weight: 700;
}
.bell__dot {
  width: calc(var(--omo-space) * 1.5);
  height: calc(var(--omo-space) * 1.5);
  margin-right: var(--omo-space);
  background: var(--omo-accent);
}
/* A text button: the top bar shrinks its buttons to icons on phones, and this one has no icon. */
.bell__mark {
  min-height: calc(var(--omo-space) * 7);
  padding: 0 var(--omo-space);
  border: 0;
  background: transparent;
  color: var(--omo-accent);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  cursor: pointer;
}
@media (hover: hover) {
  .bell__mark:hover {
    text-decoration: underline;
  }
}
.bell__foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  padding-top: calc(var(--omo-space) * 3);
  border-top: var(--omo-border) solid var(--omo-line);
}
</style>
