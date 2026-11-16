<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, useId, watch } from 'vue'
import { Terminal, type IBufferRange } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import IconButton from '../../components/ui/IconButton.vue'
import UiButton from '../../components/ui/UiButton.vue'
import Toggle from '../../components/ui/Toggle.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { logError } from '../../api/log'
import { createLinkHandler, openLink } from './terminalLinks'
import { guardPaste } from './terminalPaste'
import { usePopouts } from './popout'
import { targetOfSrc, windowPathOf, type WindowSizeKind } from './terminalTarget'
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
    /**
     * Takes the height its container gives it (a flex column): the screen
     * grows and shrinks with it, never below its minimum, and the emulator
     * fits its rows to it.
     */
    fill?: boolean
    /**
     * Offers to open the terminal in a window of its own. Off in that window
     * itself.
     */
    popout?: boolean
    /** Which remembered size the terminal's own window gets; by default the kind of terminal. */
    windowKind?: WindowSizeKind
    /** Takes the keyboard focus once it is shown. */
    autofocus?: boolean
  }>(),
  { endedText: 'The process has ended.', readonly: false, fill: false, popout: true, windowKind: undefined, autofocus: false },
)

/** The terminal as its own window knows it; null when it cannot have one. */
const target = computed(() => (props.popout ? targetOfSrc(props.src) : null))
const blocked = computed(() => target.value !== null && usePopouts().stateOf(target.value) === 'blocked')
const popoutLabel = computed(() => `Open the ${props.label.charAt(0).toLowerCase()}${props.label.slice(1)} in its own window`)

/** Opens the terminal in its own window; this view gives way to a placeholder (see `LazyTerminal`). */
function popOut(): void {
  const t = target.value
  if (!t) return
  if (usePopouts().open(t, props.windowKind ?? t.kind) === 'blocked') announcer.assertive('The browser blocked the window.')
}

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
/** The size last sent to the process, rows x columns (a hook for tests). */
const sentSize = ref('')

let term: Terminal | null = null
let fit: FitAddon | null = null
let connection: TerminalConnection | null = null
let resizeObserver: ResizeObserver | null = null
let visibleObserver: IntersectionObserver | null = null
let resizeTimer: ReturnType<typeof setTimeout> | undefined
let stopTheme: (() => void) | null = null
let stopPaste: (() => void) | null = null
let wasLive = false
/**
 * Replayed chunks the emulator has not finished parsing. Its answers to
 * queries in old output (device attributes, cursor position) would reach the
 * live process as if typed, so input is dropped while this is above zero.
 */
let replaying = 0

/** A link whose text is not its address, waiting for the user to confirm the address. */
const pendingLink = shallowRef<{ url: string; shown: string } | null>(null)

/** The text the terminal shows in `r` (1-based, end column included). */
function textOf(r: IBufferRange): string {
  const buffer = term?.buffer.active
  if (!buffer) return ''
  let out = ''
  for (let y = r.start.y; y <= r.end.y; y++) {
    const line = buffer.getLine(y - 1)
    if (!line) continue
    out += line.translateToString(true, y === r.start.y ? r.start.x - 1 : 0, y === r.end.y ? r.end.x : undefined)
  }
  return out
}

const linkHandler = createLinkHandler({ textOf, confirm: (url, shown) => (pendingLink.value = { url, shown }) })

function openPendingLink(): void {
  const link = pendingLink.value
  pendingLink.value = null
  if (link) openLink(link.url)
}

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

/** The rows and columns that fit the box, or null when it cannot be measured. */
function proposed(): { rows: number; cols: number } | null {
  const size = fit?.proposeDimensions()
  if (!size || !(size.rows >= 1) || !(size.cols >= 2)) return null
  return { rows: Math.min(size.rows, 500), cols: Math.min(size.cols, 1000) }
}

/** Fits the terminal to its box and tells the process. */
function fitNow(): void {
  if (!term || !fit) return
  let next = proposed()
  if (!next) return
  if (next.rows !== term.rows || next.cols !== term.cols) {
    term.resize(next.cols, next.rows)
    // A resize measures the font again; a font that finished loading since
    // the last measure changes the cell size, and so what fits.
    const again = proposed()
    if (again && (again.rows !== next.rows || again.cols !== next.cols)) {
      term.resize(again.cols, again.rows)
      next = again
    }
  }
  const { rows, cols } = next
  connection?.resize(rows, cols)
  sentSize.value = `${rows}x${cols}`
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

/** Waits this long at most for the font before the emulator measures its cells. */
const FONT_WAIT_MS = 3000
let unmounted = false

/**
 * Resolves once the terminal's font is loaded (or after FONT_WAIT_MS); null
 * when it is loaded already or cannot be asked. The emulator measures its
 * cells once, from the font it finds: with a fallback font the fit would be
 * wrong until the next resize (a page that has just loaded may still be
 * fetching the font while the terminal starts).
 */
function fontLoading(el: HTMLElement): Promise<void> | null {
  const fonts = typeof document !== 'undefined' ? document.fonts : undefined
  if (!fonts || typeof fonts.check !== 'function' || typeof fonts.load !== 'function') return null
  const font = `13px ${readTerminalFont(el)}`
  try {
    if (fonts.check(font)) return null
  } catch {
    return null
  }
  return Promise.race([
    fonts.load(font).then(
      () => undefined,
      () => undefined,
    ),
    new Promise<void>((resolve) => setTimeout(resolve, FONT_WAIT_MS)),
  ])
}

onMounted(() => {
  const el = host.value
  if (!el) return
  const loading = fontLoading(el)
  if (loading === null) start(el)
  else
    void loading.then(() => {
      if (!unmounted) start(el)
    })
})

function start(el: HTMLElement): void {
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
  // Pasted text may come from what an agent wrote: it is cleaned of control
  // characters before the emulator wraps it in bracketed paste.
  stopPaste = guardPaste(el, (text) => t.paste(text))
  const f = new FitAddon()
  t.loadAddon(f)
  term = t
  fit = f
  t.open(el)
  if (props.autofocus) t.focus()

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
    if (props.readonly || state.value === 'ended' || replaying > 0) return
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
    onData: (data, replay) => {
      if (!replay) {
        t.write(data)
        return
      }
      replaying++
      t.write(data, () => replaying--)
    },
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
  // Another view of the same terminal (another tab, the ticket's panel) may
  // have set its own size: this view's size is sent again when it is used.
  t.textarea?.addEventListener('focus', fitNow)
  if (typeof IntersectionObserver === 'function') {
    visibleObserver = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) fitNow()
    })
    visibleObserver.observe(el)
  }
}

onBeforeUnmount(() => {
  unmounted = true
  clearTimeout(resizeTimer)
  resizeObserver?.disconnect()
  resizeObserver = null
  visibleObserver?.disconnect()
  visibleObserver = null
  term?.textarea?.removeEventListener('focus', fitNow)
  stopTheme?.()
  stopTheme = null
  stopPaste?.()
  stopPaste = null
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
  <div class="tv" :class="{ 'tv--fill': fill }" role="group" :aria-label="label">
    <div class="tv__bar">
      <slot name="lead" />
      <span class="tv__state" :data-state="state" role="status">{{ stateText() }}</span>
      <UiButton v-if="state === 'disconnected'" size="sm" @click="connection?.reconnect()">Reconnect</UiButton>
      <span class="tv__tools">
        <Toggle v-model="screenReader" label="Screen reader mode" />
        <IconButton v-if="target && state !== 'ended'" class="tv__popout" size="sm" variant="default" :label="popoutLabel" data-terminal-popout @click="popOut">
          <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false" fill="none" stroke="currentColor" stroke-width="1.3">
            <path d="M5 1.5H1.5v9h9V7" />
            <path d="M7 1.5h3.5V5M10.5 1.5 5.5 6.5" />
          </svg>
        </IconButton>
        <slot name="trail" />
      </span>
    </div>
    <p v-if="blocked && target" class="tv__blocked" role="alert" data-terminal-blocked>
      The browser blocked the window. Allow pop-ups for this address, or open it here:
      <a :href="windowPathOf(target)" target="_blank" rel="opener" data-terminal-blocked-link>{{ label }} in a new tab</a>
    </p>
    <!-- The emulator's own box has no padding or border: the fit reads the rows from its height, which is then exactly the room for them. -->
    <div class="tv__screen" :data-readonly="readonly ? '' : undefined" :data-size="sentSize || undefined"><div ref="host" class="tv__host" /></div>
    <p v-if="state === 'ended'" class="tv__ended">{{ endedText }}</p>
    <p v-if="inputError" class="tv__error" role="alert">{{ inputError }}</p>
    <UiDialog v-if="pendingLink" open title="Open this address?" eyebrow="Terminal link" @close="pendingLink = null">
      <div class="tv__link" data-link-confirm>
        <p>The link shows “{{ visibleText(pendingLink.shown) }}” but leads to this address:</p>
        <p class="tv__target" data-link-target>{{ visibleText(pendingLink.url) }}</p>
      </div>
      <template #actions>
        <UiButton autofocus data-link-cancel @click="pendingLink = null">Cancel</UiButton>
        <UiButton variant="primary" data-link-open @click="openPendingLink">Open address</UiButton>
      </template>
    </UiDialog>
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
  gap: calc(var(--omo-space) * 2);
  min-width: 0;
  color: var(--omo-muted);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.tv__tools {
  display: flex;
  align-items: center;
  gap: calc(var(--omo-space) * 2);
  margin-left: auto;
}
/* Phones have no windows to speak of: the button is left out on narrow screens. */
@media (max-width: 767.98px) {
  .tv__popout {
    display: none;
  }
}
.tv__blocked {
  margin: 0;
  color: var(--omo-fg);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
}
.tv__blocked a {
  color: var(--omo-accent);
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
/*
 * Filling: the screen takes the room left in the column. The emulator's box
 * is placed inside it rather than in its flow, so its rows never make the
 * screen taller, and the fit (from that box's height) follows the window
 * both ways.
 */
.tv--fill {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
}
.tv--fill > .tv__screen {
  position: relative;
  flex: 1 1 0;
}
.tv--fill > .tv__screen > .tv__host {
  position: absolute;
  inset: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
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
.tv__link {
  display: grid;
  gap: calc(var(--omo-space) * 2);
  font-size: var(--omo-text-sm);
  line-height: var(--omo-text-sm-lh);
}
.tv__link p {
  margin: 0;
}
.tv__target {
  padding: calc(var(--omo-space) * 2);
  border: var(--omo-border) solid var(--omo-line);
  overflow-wrap: anywhere;
}
.tv__hint kbd {
  padding: 0 var(--omo-space);
  border: var(--omo-border) solid var(--omo-line);
  color: var(--omo-fg);
  font: inherit;
}
</style>
