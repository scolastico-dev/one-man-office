<script setup lang="ts">
// The ticket panel with its tabs (Comments, Diff, Agents) filled with static
// data. The panel itself is the real one, reading the sample tickets through
// a sample session; only the tabs' content is drawn here.
import { computed, provide, ref } from 'vue'
import type { DiffFile, DiffFileStatus } from '../api/types'
import LiveDot from '../components/ui/LiveDot.vue'
import RelativeTime from '../components/ui/RelativeTime.vue'
import StatusBadge from '../components/ui/StatusBadge.vue'
import TextArea from '../components/ui/TextArea.vue'
import UiButton from '../components/ui/UiButton.vue'
import UiTabs, { type UiTab } from '../components/ui/UiTabs.vue'
import { visibleText } from '../components/ui/visibleText'
import TicketPanel from '../features/ticket/TicketPanel.vue'
import { SESSION, type Session } from '../session'
import { useBoard } from '../store'
import { minutesAgo, sampleEndedSession, sampleThread } from './sampleBoard'
import { patchRows, sampleDiff } from './sampleDiff'
import { sampleClient } from './sampleStore'
import SampleTerminal from './SampleTerminal.vue'

const props = defineProps<{ number: number }>()
const emit = defineEmits<{ close: [] }>()
const tab = defineModel<string>('tab', { required: true })

const store = useBoard()
const session: Session = {
  phase: ref('ready'),
  lockReason: ref('missing'),
  problem: ref(''),
  client: sampleClient,
  store,
  start() {},
  retry() {},
  unlock() {},
  addressChanged() {},
}
provide(SESSION, session)

const project = sampleDiff.projects[0]!
const comments = sampleThread.filter((x) => x.kind === 'comment').length
const tabs = computed<UiTab[]>(() => [
  { id: 'comments', label: 'Comments', count: comments },
  { id: 'diff', label: 'Diff', count: project.files.length + project.uncommitted.length },
  { id: 'agents', label: 'Agents', count: 2 },
])

const STATUS: Record<DiffFileStatus, [letter: string, words: string]> = {
  added: ['A', 'added'],
  modified: ['M', 'modified'],
  deleted: ['D', 'deleted'],
  renamed: ['R', 'renamed'],
  copied: ['C', 'copied'],
  type_changed: ['T', 'type changed'],
  untracked: ['?', 'untracked'],
}
const additions = project.files.reduce((n, f) => n + f.additions, 0)
const deletions = project.files.reduce((n, f) => n + f.deletions, 0)
const shown = (f: DiffFile) => f.patch !== null
const calmSince = minutesAgo(64)
</script>

<template>
  <TicketPanel :number="props.number" @close="emit('close')">
    <template #tabs>
      <section class="ptabs" aria-label="Activity">
        <UiTabs v-model="tab" :tabs="tabs" :label="`Activity on #${props.number}`">
          <template #panel="{ active }">
            <div class="ptabs__panel">
              <!-- Comments -->
              <template v-if="active === 'comments'">
                <ol class="thread">
                  <li v-for="item in sampleThread" :key="item.id">
                    <p v-if="item.kind === 'status'" class="thread__status">
                      <span>{{ item.text }}</span> <RelativeTime class="thread__time" :at="item.at" />
                    </p>
                    <article v-else class="cmt" :class="{ 'cmt--question': item.question === 'open' }" :aria-label="`Comment by ${item.author}`">
                      <header class="cmt__head">
                        <span class="cmt__author">{{ item.author }}</span>
                        <RelativeTime class="thread__time" :at="item.at" />
                        <StatusBadge v-if="item.question === 'open'" tone="warn" text="question for you" />
                      </header>
                      <p class="cmt__body">
                        <template v-for="(part, i) in item.parts" :key="i"
                          ><code v-if="part.code">{{ part.text }}</code
                          ><span v-else-if="part.mention" class="mention" :class="{ 'mention--you': part.mention === 'you' }">{{ part.text }}</span
                          ><template v-else>{{ part.text }}</template></template
                        >
                      </p>
                      <div v-if="item.question === 'open'" class="cmt__actions">
                        <UiButton size="sm" variant="primary">Answer</UiButton>
                      </div>
                    </article>
                  </li>
                </ol>
                <form class="composer" @submit.prevent>
                  <TextArea
                    label="Write a comment"
                    :rows="3"
                    hint="Mention a role to reach its agent: @developer is calm-badger, @pm is quiet-heron."
                  />
                  <div class="composer__actions">
                    <UiButton>Ask a question…</UiButton>
                    <UiButton variant="primary" type="submit">Comment</UiButton>
                  </div>
                </form>
              </template>

              <!-- Diff -->
              <template v-else-if="active === 'diff'">
                <div class="sdiff">
                  <header class="sdiff__head">
                    <h4 class="sdiff__project">github.com/not-three/ui</h4>
                    <p class="sdiff__sum">
                      <code>{{ project.branch }}</code> against <code>{{ project.base }}</code> · {{ project.files.length }} files ·
                      <span class="sdiff__add">+{{ additions }}</span> <span class="sdiff__del">−{{ deletions }}</span>
                    </p>
                  </header>
                  <template v-for="(group, g) in [project.files, project.uncommitted]" :key="g">
                    <h5 v-if="g === 1" class="sdiff__group">Not committed yet</h5>
                    <ul class="sdiff__list">
                      <li v-for="f in group" :key="f.path" class="dfile">
                        <div class="dfile__head">
                          <span class="dfile__status" :class="`dfile__status--${f.status}`"
                            ><span aria-hidden="true">{{ STATUS[f.status][0] }}</span
                            ><span class="sr-only">{{ STATUS[f.status][1] }}</span></span
                          >
                          <span class="dfile__path"
                            >{{ visibleText(f.path) }}<span v-if="f.old_path" class="dfile__old"> ← {{ visibleText(f.old_path) }}</span></span
                          >
                          <span v-if="f.binary" class="dfile__note">binary</span>
                          <span v-else-if="f.status === 'untracked'" class="dfile__note">contents not shown</span>
                          <span v-else class="dfile__counts"
                            ><span class="sdiff__add">+{{ f.additions }}</span> <span class="sdiff__del">−{{ f.deletions }}</span></span
                          >
                        </div>
                        <div v-if="shown(f)" class="dfile__body" tabindex="0" role="region" :aria-label="`Changes in ${visibleText(f.path)}`">
                          <div class="dfile__rows">
                          <div
                            v-for="(row, r) in patchRows(f.patch!)"
                            :key="r"
                            class="drow"
                            :class="`drow--${row.kind}`"
                          >
                            <template v-if="row.kind === 'hunk'">
                              <span class="drow__hunk">{{ row.text }}</span>
                            </template>
                            <template v-else>
                              <span class="drow__no">{{ row.old ?? '' }}</span>
                              <span class="drow__no">{{ row.new ?? '' }}</span>
                              <span class="drow__sign"
                                ><span aria-hidden="true">{{ row.kind === 'add' ? '+' : row.kind === 'del' ? '−' : ' ' }}</span
                                ><span v-if="row.kind !== 'same'" class="sr-only">{{ row.kind === 'add' ? 'added' : 'removed' }}</span></span
                              >
                              <span class="drow__text">{{ row.text }}</span>
                            </template>
                          </div>
                          </div>
                        </div>
                      </li>
                    </ul>
                  </template>
                </div>
              </template>

              <!-- Agents -->
              <template v-else>
                <div class="sagents">
                  <div class="sagent">
                    <p class="sagent__head">
                      <LiveDot state="waiting" />
                      <span><span class="sagent__role">developer</span> · calm-badger</span>
                      <span class="sagent__muted">parked · since <RelativeTime :at="calmSince" /></span>
                    </p>
                    <SampleTerminal label="Terminal of developer calm-badger" title="claude · developer · #13" screen="agent" note="Typing here types into the agent." />
                  </div>
                  <div class="sagent sagent--ended">
                    <p class="sagent__head">
                      <span><span class="sagent__role">developer</span> · {{ sampleEndedSession.name }}</span>
                      <span class="sagent__muted">ended <RelativeTime :at="sampleEndedSession.ended" />, {{ sampleEndedSession.reason }}</span>
                    </p>
                    <UiButton size="sm">Show the saved log</UiButton>
                  </div>
                </div>
              </template>
            </div>
          </template>
        </UiTabs>
      </section>
    </template>
  </TicketPanel>
</template>

<style scoped>
.ptabs {
  margin-top: calc(var(--omo-space) * 8);
}
.ptabs__panel {
  padding-top: calc(var(--omo-space) * 5);
}

/* Comments */
.thread {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.thread__status {
  margin: 0;
  padding-left: calc(var(--omo-space) * 3);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.thread__time {
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.cmt {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  padding: calc(var(--omo-space) * 3);
  border: var(--omo-border) solid var(--omo-line);
}
.cmt--question {
  border-color: var(--omo-warn);
  border-left-width: var(--omo-edge);
}
.cmt__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--omo-space) calc(var(--omo-space) * 2);
}
.cmt__author {
  font-size: var(--omo-text-sm);
  font-weight: 700;
  line-height: var(--omo-text-sm-lh);
}
.cmt__body {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
  overflow-wrap: anywhere;
}
.cmt__body code {
  padding: 0 var(--omo-space);
  background: var(--omo-raised);
}
.mention {
  padding: 0 calc(var(--omo-space) * 0.5);
  background: var(--omo-mention);
}
.mention--you {
  font-weight: 700;
}
.cmt__actions,
.composer__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: calc(var(--omo-space) * 2);
}
.cmt__actions {
  justify-content: flex-start;
}
.composer {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin-top: calc(var(--omo-space) * 5);
  padding-top: calc(var(--omo-space) * 5);
  border-top: var(--omo-border) solid var(--omo-line);
}

/* Diff */
.sdiff {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  min-width: 0;
}
.sdiff__head {
  display: grid;
  gap: var(--omo-space);
}
.sdiff__project,
.sdiff__group {
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.sdiff__group {
  margin-top: calc(var(--omo-space) * 2);
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  font-weight: 400;
  line-height: var(--omo-text-xs-lh);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.sdiff__sum {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.sdiff__sum code {
  color: var(--omo-fg);
}
.sdiff__add {
  color: var(--omo-ok);
}
.sdiff__del {
  color: var(--omo-danger);
}
.sdiff__list {
  display: grid;
  gap: calc(var(--omo-space) * 3);
  margin: 0;
  padding: 0;
  list-style: none;
}
.dfile {
  min-width: 0;
  border: var(--omo-border) solid var(--omo-line);
}
.dfile__head {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  padding: calc(var(--omo-space) * 1.5) calc(var(--omo-space) * 2.5);
  background: var(--omo-raised);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.dfile__status {
  flex-shrink: 0;
  width: 2ch;
  color: var(--omo-muted);
  font-weight: 700;
}
.dfile__status--added,
.dfile__status--untracked {
  color: var(--omo-ok);
}
.dfile__status--deleted {
  color: var(--omo-danger);
}
.dfile__status--modified,
.dfile__status--renamed {
  color: var(--omo-accent);
}
.dfile__path {
  flex: 1 1 auto;
  min-width: 0;
  overflow-wrap: anywhere;
}
.dfile__old {
  color: var(--omo-muted);
}
.dfile__counts,
.dfile__note {
  flex-shrink: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.dfile__note {
  color: var(--omo-muted);
}
.dfile__body {
  overflow-x: auto;
  border-top: var(--omo-border) solid var(--omo-line);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.dfile__body:focus-visible {
  outline-offset: calc(var(--omo-focus-width) * -1);
}
/* As wide as the longest line, so every row's fill reaches the end when the box scrolls. */
.dfile__rows {
  width: max-content;
  min-width: 100%;
}
.drow {
  display: grid;
  grid-template-columns: 5ch 5ch 2ch max-content;
  white-space: pre;
}
.drow--add {
  background: var(--omo-diff-add);
}
.drow--del {
  background: var(--omo-diff-del);
}
.drow--hunk {
  display: block;
  background: var(--omo-diff-hunk);
  color: var(--omo-muted);
}
.drow__hunk {
  display: block;
  padding: 0 calc(var(--omo-space) * 2.5);
}
.drow__no {
  padding-right: calc(var(--omo-space) * 2);
  color: var(--omo-muted);
  text-align: right;
  user-select: none;
}
.drow__sign {
  user-select: none;
}
.drow--add .drow__sign {
  color: var(--omo-ok);
}
.drow--del .drow__sign {
  color: var(--omo-danger);
}
.drow__text {
  padding-right: calc(var(--omo-space) * 3);
}

/* Agents */
.sagents {
  display: grid;
  gap: calc(var(--omo-space) * 5);
}
.sagent {
  display: grid;
  justify-items: start;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
}
.sagent > :deep(.sterm) {
  justify-self: stretch;
}
.sagent--ended {
  padding-top: calc(var(--omo-space) * 5);
  border-top: var(--omo-border) solid var(--omo-line);
}
.sagent__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0 calc(var(--omo-space) * 2);
  margin: 0;
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.sagent__role,
.sagent__muted {
  color: var(--omo-muted);
}
.sagent__muted {
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
</style>
