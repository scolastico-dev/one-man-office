<script setup lang="ts">
// A terminal as it will look, drawn statically: no xterm, no connection.
// The lines use the six ANSI hues, plain and bright, so both themes can be
// judged; black and white are left out, as they are not meant to be read on
// every ground.
import { visibleText } from '../components/ui/visibleText'

withDefaults(
  defineProps<{
    /** Accessible name of the terminal. */
    label: string
    /** The terminal's title, as the program set it. */
    title: string
    /** Which lines to show. */
    screen?: 'shell' | 'agent'
    /** The note under the terminal, e.g. who receives what is typed. */
    note?: string
  }>(),
  { screen: 'shell', note: undefined },
)

type Ansi =
  | 'black' | 'red' | 'green' | 'yellow' | 'blue' | 'magenta' | 'cyan' | 'white'
  | 'bright-black' | 'bright-red' | 'bright-green' | 'bright-yellow' | 'bright-blue' | 'bright-magenta' | 'bright-cyan' | 'bright-white'
type Line = [text: string, colour?: Ansi, bold?: boolean][]

const prompt = (cwd: string, branch: string): Line => [
  [cwd, 'blue'],
  [' ('],
  [branch, 'magenta'],
  [') '],
  ['$ '],
]

const shell: Line[] = [
  [...prompt('~/.local/omo/worktrees/omo/13', 'omo/13'), ['go test ./internal/board']],
  [['ok  ', 'green', true], ['  internal/board  0.412s']],
  [['--- FAIL: ', 'red', true], ['TestDragTargets/story_with_busy_task (0.01s)']],
  [['    drag_test.go:88: '], ['want '], ['ready', 'bright-green'], [', got '], ['planning', 'bright-red']],
  [['FAIL', 'red', true]],
  [...prompt('~/.local/omo/worktrees/omo/13', 'omo/13'), ['git status --short']],
  [[' M ', 'yellow'], ['web/src/features/board/useDrag.ts', 'cyan']],
  [['?? ', 'bright-yellow'], ['web/src/features/board/DragGhost.vue', 'bright-cyan']],
  [['warning: ', 'yellow', true], ['LF will be replaced by CRLF the next time Git touches it']],
  [['hint: ', 'bright-blue'], ['use "git add" to track'], [' · '], ['3 commits ahead', 'bright-magenta']],
  [...prompt('~/.local/omo/worktrees/omo/13', 'omo/13')],
]

const agent: Line[] = [
  [['● ', 'magenta'], ['Reading '], ['web/src/features/board/useDrag.ts', 'cyan']],
  [['● ', 'magenta'], ['Running '], ['npm --prefix web run test -- src/features/board']],
  [['  ✓ ', 'green'], ['useDrag.test.ts '], ['(14 tests) 38ms']],
  [['  ✗ ', 'red'], ['BoardView.test.ts '], ['(1 failed)', 'bright-red']],
  [['● ', 'magenta'], ['Asked '], ['@user', 'yellow', true], [' a question on '], ['#13', 'bright-blue'], ['; waiting with '], ['omo ai wait', 'bright-cyan']],
  [],
  [['> ']],
]
</script>

<template>
  <section class="sterm" :aria-label="label">
    <div class="sterm__bar">
      <span class="sterm__title">{{ visibleText(title) }}</span>
      <span class="sterm__state">connected</span>
    </div>
    <pre class="sterm__screen" tabindex="0"><template v-for="(line, i) in screen === 'agent' ? agent : shell" :key="i"><span
      v-for="([text, colour, bold], j) in line"
      :key="j"
      :class="[colour ? `sterm--${colour}` : '', bold ? 'sterm--bold' : '']"
    >{{ text }}</span><span v-if="i === (screen === 'agent' ? agent : shell).length - 1" class="sterm__cursor" aria-hidden="true"> </span>
</template></pre>
    <p class="sterm__note">
      <span v-if="note">{{ `${note} ` }}</span><kbd>F6</kbd> leaves the terminal, <kbd>Shift+F6</kbd> returns to it.
    </p>
  </section>
</template>

<style scoped>
.sterm {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.sterm__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.sterm__title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sterm__state {
  flex-shrink: 0;
  color: var(--omo-ok);
}
.sterm__screen {
  min-width: 0;
  min-height: calc(var(--omo-space) * 60);
  margin: 0;
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  overflow: auto;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-term-bg);
  color: var(--omo-term-fg);
  font-size: var(--omo-text-sm);
  line-height: 1.4;
  white-space: pre;
}
.sterm__screen:focus-visible {
  border-color: var(--omo-accent);
  outline-offset: calc(var(--omo-focus-width) * -1);
}
.sterm__screen::selection,
.sterm__screen ::selection {
  background: var(--omo-term-selection);
}
.sterm__cursor {
  background: var(--omo-term-cursor);
}
.sterm__note {
  margin: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.sterm__note kbd {
  padding: 0 var(--omo-space);
  border: var(--omo-border) solid var(--omo-line);
  color: var(--omo-fg);
  font: inherit;
}
.sterm--bold { font-weight: 700; }
.sterm--black { color: var(--omo-ansi-black); }
.sterm--red { color: var(--omo-ansi-red); }
.sterm--green { color: var(--omo-ansi-green); }
.sterm--yellow { color: var(--omo-ansi-yellow); }
.sterm--blue { color: var(--omo-ansi-blue); }
.sterm--magenta { color: var(--omo-ansi-magenta); }
.sterm--cyan { color: var(--omo-ansi-cyan); }
.sterm--white { color: var(--omo-ansi-white); }
.sterm--bright-black { color: var(--omo-ansi-bright-black); }
.sterm--bright-red { color: var(--omo-ansi-bright-red); }
.sterm--bright-green { color: var(--omo-ansi-bright-green); }
.sterm--bright-yellow { color: var(--omo-ansi-bright-yellow); }
.sterm--bright-blue { color: var(--omo-ansi-bright-blue); }
.sterm--bright-magenta { color: var(--omo-ansi-bright-magenta); }
.sterm--bright-cyan { color: var(--omo-ansi-bright-cyan); }
.sterm--bright-white { color: var(--omo-ansi-bright-white); }
</style>
