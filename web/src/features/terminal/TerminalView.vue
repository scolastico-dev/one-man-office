<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import UiButton from '../../components/ui/UiButton.vue'
import Toggle from '../../components/ui/Toggle.vue'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { logError } from '../../api/log'
import { linkHandler } from './terminalLinks'
import { createTerminalConnection, type ConnectionState, type TerminalConnection } from './terminalSocket'
import { readTerminalFont, readTerminalTheme, watchTheme } from './terminalTheme'

const props = withDefaults(
  defineProps<{
    /** Websocket path from `agentTerminalPath` or `userTerminalPath`. */
    src: string
    /** Accessible name of the terminal. */
    label: string
    /** Shown when the process has ended for good. */
    endedText?: string
    /** Output only: typing and pasting do nothing. */
    readonly?: boolean
  }>(),
  { endedText: 'The process has ended.', readonly: false },
)

const emit = defineEmits<{ state: [state: ConnectionState] }>()

const SCREEN_READER_KEY = 'omo.terminal.screenReader'
const RESIZE_DEBOUNCE_MS = 100

const announcer = useAnnouncer()
const session = useSession()
const hintId = useId()
const host = ref<HTMLElement | null>(null)
const state = ref<ConnectionState>('connecting')
const retryMs = ref<number | undefined>(undefined)
const inputError = ref('')
const screenReader = ref(readScreenReader())

let term: Terminal | null = null
let fit: FitAddon | null = null
let connection: TerminalConnection | null = null
let resizeObserver: ResizeObserver | null = null
let resizeTimer: ReturnType<typeof setTimeout> | undefined
let stopTheme: (() => void) | null = null
let wasLive = false

function readScreenReader(): boolean {
  try {
    return window.localStorage.getItem(SCREEN_READER_KEY) === '1'
  } catch {
    return false
  }
}

watch(screenReader, (on) => {
  if (term) term.options.screenReaderMode = on
  try {
    window.localStorage.setItem(SCREEN_READER_KEY, on ? '1' : '0')
  } catch {
    // Not remembered; the toggle still works for this page.
  }
})

const stateText = (): string => {
  switch (state.value) {
    case 'connecting':
      return 'Connecting'
    case 'live':
      return 'Connected'
    case 'ended':
      return 'Ended'
    default:
      return retryMs.value === undefined
        ? 'Disconnected'
        : `Disconnected, trying again in ${Math.max(1, Math.round(retryMs.value / 1000))} s`
  }
}

function onState(next: ConnectionState, wait?: number): void {
  retryMs.value = wait
  if (next === state.value && wait === undefined) return
  const previous = state.value
  state.value = next
  if (next === 'live') {
    inputError.value = ''
    if (wasLive) announcer.polite(`${props.label}: connected again.`)
    wasLive = true
  } else if (next === 'disconnected' && previous !== 'disconnected') {
    announcer.assertive(`${props.label}: disconnected.`)
  } else if (next === 'ended') {
    announcer.polite(`${props.label}: ${props.endedText}`)
  }
  if (term) term.options.disableStdin = props.readonly || next === 'ended'
  if (next !== previous) emit('state', next)
}

/** Fits the terminal to its box and tells the process. */
function fitNow(): void {
  if (!term || !fit) return
  const size = fit.proposeDimensions()
  if (!size || !(size.rows >= 1) || !(size.cols >= 2)) return
  const rows = Math.min(size.rows, 500)
  const cols = Math.min(size.cols, 1000)
  if (rows !== term.rows || cols !== term.cols) term.resize(cols, rows)
  connection?.resize(rows, cols)
}

function scheduleFit(): void {
  clearTimeout(resizeTimer)
  resizeTimer = setTimeout(fitNow, RESIZE_DEBOUNCE_MS)
}

const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/** Moves focus to the next (or previous) focusable element outside this terminal's screen. */
function leave(backwards: boolean): void {
  const screen = host.value
  if (!screen) return
  const candidates = Array.from(document.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => !screen.contains(el) && !el.contains(screen) && !el.closest('[inert], [hidden]') && el.getClientRects().length > 0,
  )
  const wanted = backwards ? Node.DOCUMENT_POSITION_PRECEDING : Node.DOCUMENT_POSITION_FOLLOWING
  const around = candidates.filter((el) => screen.compareDocumentPosition(el) & wanted)
  const target = backwards ? around[around.length - 1] : around[0]
  if (target) target.focus()
  else (document.activeElement as HTMLElement | null)?.blur()
}

onMounted(() => {
  const el = host.value
  if (!el) return
  const reduced = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  const t = new Terminal({
    allowProposedApi: false,
    scrollback: 5000,
    fontFamily: readTerminalFont(el),
    fontSize: 13,
    cursorBlink: !reduced,
    screenReaderMode: screenReader.value,
    disableStdin: props.readonly,
    theme: readTerminalTheme(el),
    linkHandler,
    windowOptions: {},
  })
  const f = new FitAddon()
  t.loadAddon(f)
  term = t
  fit = f
  t.open(el)

  // Output is untrusted. What it could use to act on the page or the system
  // is swallowed: titles, the clipboard (OSC 52) and palette or colour changes.
  for (const code of [0, 1, 2, 4, 10, 11, 12, 52, 104, 110, 111, 112]) t.parser.registerOscHandler(code, () => true)

  if (t.textarea) {
    t.textarea.setAttribute('aria-label', props.label)
    t.textarea.setAttribute('aria-describedby', hintId)
  }
  t.attachCustomKeyEventHandler((ev) => {
    if (ev.key !== 'F6') return true
    if (ev.type === 'keydown') {
      ev.preventDefault()
      leave(ev.shiftKey)
    }
    return false
  })
  t.onData((data) => {
    if (props.readonly || state.value === 'ended') return
    inputError.value = ''
    connection?.send(data)
  })

  stopTheme = watchTheme(() => {
    if (!term || !host.value) return
    term.options.theme = readTerminalTheme(host.value)
  })

  connection = createTerminalConnection({
    path: props.src,
    getToken: () => session.token(),
    onData: (data) => t.write(data),
    onState,
    onError: (text) => {
      inputError.value = text
      announcer.assertive(text)
    },
  })
  fitNow()

  if (typeof ResizeObserver === 'function') {
    resizeObserver = new ResizeObserver(scheduleFit)
    resizeObserver.observe(el)
  }
})

onBeforeUnmount(() => {
  clearTimeout(resizeTimer)
  resizeObserver?.disconnect()
  resizeObserver = null
  stopTheme?.()
  stopTheme = null
  connection?.dispose()
  connection = null
  try {
    term?.dispose()
  } catch (e) {
    logError('closing a terminal', e)
  }
  term = null
  fit = null
})

defineExpose({
  /** Puts the keyboard focus into the terminal. */
  focus: () => term?.focus(),
})
</script>

<template>
  <div class="tv" role="group" :aria-label="label">
    <div class="tv__bar">
      <span class="tv__state" :data-state="state" role="status">{{ stateText() }}</span>
      <UiButton v-if="state === 'disconnected'" size="sm" @click="connection?.reconnect()">Reconnect</UiButton>
      <Toggle v-model="screenReader" label="Screen reader mode" />
    </div>
    <div ref="host" class="tv__screen" :data-readonly="readonly ? '' : undefined" />
    <p v-if="state === 'ended'" class="tv__ended">{{ endedText }}</p>
    <p v-if="inputError" class="tv__error" role="alert">{{ inputError }}</p>
    <p :id="hintId" class="tv__hint">
      <template v-if="readonly">Read only. </template><kbd>F6</kbd> leaves the terminal, <kbd>Shift+F6</kbd> goes to the element before it.
    </p>
  </div>
</template>

<style scoped>
.tv {
  display: grid;
  gap: calc(var(--omo-space) * 1.5);
  min-width: 0;
}
.tv__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.tv__state[data-state='live'] {
  color: var(--omo-ok);
}
.tv__screen {
  min-width: 0;
  min-height: calc(var(--omo-space) * 60);
  padding: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
  overflow: hidden;
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-term-bg);
}
.tv__screen:focus-within {
  border-color: var(--omo-accent);
}
.tv__ended,
.tv__error,
.tv__hint {
  margin: 0;
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.tv__ended,
.tv__hint {
  color: var(--omo-muted);
}
.tv__error {
  color: var(--omo-danger);
}
.tv__hint kbd {
  padding: 0 var(--omo-space);
  border: var(--omo-border) solid var(--omo-line);
  color: var(--omo-fg);
  font: inherit;
}
</style>
