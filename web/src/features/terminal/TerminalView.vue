<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, useId, watch } from 'vue'
import { Terminal, type IBufferRange } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import IconButton from '../../components/ui/IconButton.vue'
import UiButton from '../../components/ui/UiButton.vue'
import UiDialog from '../../components/ui/UiDialog.vue'
import { visibleText } from '../../components/ui/visibleText'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import { logError } from '../../api/log'
import { createLinkHandler, openLink } from './terminalLinks'
import { cleanPaste, guardPaste } from './terminalPaste'
import { clipboardKey, execCopy, parseOsc52, writeClipboard } from './terminalClipboard'
import { lineBreakKey } from './terminalKeys'
import { pastedPaths } from './terminalImages'
import { uploadImages } from './terminalUploads'
import { isAgentSrc, isWritable, setWritable } from './terminalWritable'
import TerminalDraft from './TerminalDraft.vue'
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

/**
 * An agent's terminal is read-only until the user switches it: its keyboard,
 * paste and dropped files do nothing (the mouse works as ever, and a program
 * that asks for mouse events gets them), and the draft box below is where
 * messages are written. The choice is kept per terminal for the browser session.
 */
const agentTerminal = computed(() => isAgentSrc(props.src))
const writable = ref(isWritable(props.src))
const sendingDraft = ref(false)
const draftBox = ref<InstanceType<typeof TerminalDraft> | null>(null)
const locked = computed(() => props.readonly || sendingDraft.value || (agentTerminal.value && !writable.value))
const draftId = computed(() => targetOfSrc(props.src)?.id ?? props.src)
/** The draft box is for a terminal that takes input and has not ended. */
const showDraft = computed(() => agentTerminal.value && !props.readonly && state.value !== 'ended')

function toggleWritable(): void {
  writable.value = !writable.value
  setWritable(props.src, writable.value)
  announcer.polite(writable.value ? 'The terminal takes typing.' : 'The terminal is read-only.')
  if (writable.value) term?.focus()
}

/** One message from the draft box, written by omo as a whole (omo adds the paste markers the program asked for). */
async function sendDraft(parts: string[]): Promise<void> {
  if (!connection) throw new Error('The terminal is not connected.')
  sendingDraft.value = true
  try {
    await connection.sendMessage(parts)
  } finally {
    sendingDraft.value = false
  }
}

let press: { x: number; y: number; dragged: boolean } | null = null
function screenPress(ev: PointerEvent): void {
  press = ev.button === 0 ? { x: ev.clientX, y: ev.clientY, dragged: false } : null
}
function screenMove(ev: PointerEvent): void {
  if (press && (ev.clientX !== press.x || ev.clientY !== press.y)) press.dragged = true
}
function screenClick(ev: MouseEvent): void {
  const click = press
  press = null
  if (!showDraft.value || writable.value || !click || click.dragged || ev.button !== 0 || ev.detail !== 1 || ev.ctrlKey || ev.metaKey || ev.shiftKey || ev.altKey) return
  // Controls and the native scrollbar keep their own focus behavior.
  if ((ev.target as Element).closest('button, a, .xterm-viewport')) return
  // The emulator finishes handling this click first, including its focus work.
  queueMicrotask(() => draftBox.value?.focus())
}

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

const RESIZE_DEBOUNCE_MS = 100
/**
 * A program may set the clipboard (OSC 52) only this long after the user's
 * last key or pointer press in this terminal: a selection made in the
 * program, or its copy command. Output that arrives on its own, or replayed
 * history, never changes the clipboard.
 */
const OSC52_AFTER_INPUT_MS = 5000
/** How long a short note ("Copied") stays. */
const NOTE_MS = 2500

const announcer = useAnnouncer()
const session = useSession()
const hintId = useId()
const host = ref<HTMLElement | null>(null)
const screen = ref<HTMLElement | null>(null)
const state = ref<ConnectionState>('connecting')
const retryMs = ref<number | undefined>(undefined)
const inputError = ref('')
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
let stopPointer: (() => void) | null = null
let wasLive = false
/** When the user last pressed a key or a pointer in this terminal (ms). */
let lastInput = Number.NEGATIVE_INFINITY

/** Text a program handed over that the browser would not copy without a gesture; copied on the next one. */
const pendingCopy = ref<string | null>(null)
/** A short note in the corner of the screen: copied, uploading, a refusal. */
const note = ref<{ text: string; error: boolean } | null>(null)
let noteTimer: ReturnType<typeof setTimeout> | undefined
/** Files are dragged over the screen. */
const dropping = ref(false)
let dragDepth = 0

function showNote(text: string, error = false, stay = false): void {
  clearTimeout(noteTimer)
  note.value = { text, error }
  if (!stay) noteTimer = setTimeout(() => (note.value = null), error ? NOTE_MS * 3 : NOTE_MS)
}

function copied(): void {
  pendingCopy.value = null
  showNote('Copied')
  announcer.polite('Copied to the clipboard.')
}

/** Copies text now or, when the browser refuses, keeps it for the next gesture. */
async function copyText(text: string): Promise<void> {
  pendingCopy.value = null
  if (await writeClipboard(text)) {
    copied()
    return
  }
  pendingCopy.value = text
  announcer.polite('The browser copies only on a click or key: press Copy or any key in the terminal.')
}

/** Inside a key or pointer handler: copies the text kept for a gesture. */
function copyPendingNow(): void {
  const text = pendingCopy.value
  if (text !== null && execCopy(text)) copied()
}

function copyPendingClick(): void {
  const text = pendingCopy.value
  if (text === null) return
  if (execCopy(text)) copied()
  else void copyText(text)
  term?.focus()
}

/** OSC 52 from the program: a set of the clipboard shortly after the user's input. */
function takeOsc52(data: string): void {
  if (replaying > 0 || props.readonly || performance.now() - lastInput > OSC52_AFTER_INPUT_MS) return
  const got = parseOsc52(data)
  if (got.kind === 'set') void copyText(got.text)
}

/** A clipboard key (see clipboardKey); returns what the emulator should do. */
function clipboardKeyPressed(t: Terminal, ev: KeyboardEvent): boolean | undefined {
  const action = clipboardKey(ev, t.hasSelection())
  if (action === 'paste') return false // the browser's paste; guardPaste takes it
  if (action === null) return undefined
  if (ev.type === 'keydown') {
    ev.preventDefault()
    const text = t.hasSelection() ? t.getSelection() : pendingCopy.value
    if (text) {
      if (execCopy(text)) copied()
      else void copyText(text)
    }
    if (action === 'copy-clear') t.clearSelection()
  }
  return false
}

/** Typed input on its way to the program. */
function sendInput(data: string): void {
  if (props.readonly || state.value === 'ended') return
  if (replaying > 0 && fromUser === 0) return
  inputError.value = ''
  connection?.send(data)
}

/** Runs `act`; what the emulator hands over while it runs is the user's own input. */
function asUser(act: () => void): void {
  fromUser++
  try {
    act()
  } finally {
    fromUser--
  }
}

/**
 * A key the emulator is handling: it hands the key's text over in the same
 * turn, before any microtask runs, and it parses output only in turns of its
 * own, so until then what arrives is the user's.
 */
function keyTurn(): void {
  fromUser++
  queueMicrotask(() => fromUser--)
}

/** Images dropped or pasted into the terminal: stored by omo, their paths pasted. */
async function takeFiles(list: File[]): Promise<void> {
  if (locked.value || state.value === 'ended' || !term) return
  const { stored, problems } = await uploadImages(session.client, list, (n) =>
    showNote(n === 1 ? 'Uploading the image…' : `Uploading ${n} images…`, false, true),
  )
  if (unmounted || !term || locked.value) return
  if (stored.length === 0 && problems.length > 0) {
    showNote(problems.join(' '), true)
    announcer.assertive(problems.join(' '))
    return
  }
  if (stored.length > 0) {
    const text = cleanPaste(pastedPaths(stored.map((s) => s.path)))
    asUser(() => term?.paste(text))
    if (!locked.value) term.focus()
  }
  if (problems.length > 0) {
    showNote(problems.join(' '), true)
    announcer.assertive(problems.join(' '))
  } else {
    note.value = null
  }
}

function takesFiles(): boolean {
  return !locked.value && state.value !== 'ended'
}

function hasFiles(ev: DragEvent): boolean {
  return ev.dataTransfer?.types.includes('Files') === true
}

function onDragEnter(ev: DragEvent): void {
  if (!hasFiles(ev) || !takesFiles()) return
  dragDepth++
  dropping.value = true
}

function onDragOver(ev: DragEvent): void {
  if (hasFiles(ev) && ev.dataTransfer) ev.dataTransfer.dropEffect = takesFiles() ? 'copy' : 'none'
}

function onDragLeave(ev: DragEvent): void {
  if (!hasFiles(ev) || dragDepth === 0) return
  if (--dragDepth === 0) dropping.value = false
}

function onDropEnd(): void {
  dragDepth = 0
  dropping.value = false
}
/**
 * Replayed chunks the emulator has not finished parsing. Its answers to
 * queries in old output (device attributes, cursor position) would reach the
 * live process as if typed, so what the emulator hands over is dropped while
 * this is above zero, unless the user is typing or pasting right then.
 */
let replaying = 0

/**
 * Above zero while a key or a paste of the user is being handled. Without it
 * the first keys typed into a terminal that just opened are lost whenever its
 * replay is still being parsed.
 */
let fromUser = 0

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

/** The global setting: screen reader software gets a text copy of the output (settings, General). */
const screenReader = computed(() => session.store.state.general?.ui.screen_reader_mode === true)
watch(screenReader, (on) => {
  if (term) term.options.screenReaderMode = on
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

/**
 * The board's own connection retries for as long as the page is open: once it
 * is live again omo is reachable, so a terminal that lost its connection (or
 * stopped trying) connects again on its own.
 */
watch(
  () => session.store.connection.value,
  (now, before) => {
    if (now === 'live' && before !== 'live' && state.value === 'disconnected') connection?.reconnect()
  },
)

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
  // characters before the emulator wraps it in bracketed paste. Dropped or
  // pasted image files are stored by omo and their paths pasted.
  const box = screen.value ?? el
  stopPaste = guardPaste(
    box,
    (text) => {
      if (!locked.value) asUser(() => t.paste(text))
    },
    (list) => void takeFiles(list),
  )
  const onPointer = () => {
    lastInput = performance.now()
    copyPendingNow()
  }
  box.addEventListener('pointerdown', onPointer, true)
  // Text that reaches the emulator without a key it handles (an input method,
  // a mobile keyboard) is stopped while the terminal is read-only. Mouse events
  // are not touched.
  const textEvents = ['keypress', 'beforeinput', 'input', 'compositionstart', 'compositionupdate', 'compositionend']
  const blockText = (ev: Event) => {
    if (!locked.value) return
    ev.stopPropagation()
    if (ev.cancelable) ev.preventDefault()
  }
  for (const name of textEvents) box.addEventListener(name, blockText, true)
  stopPointer = () => {
    box.removeEventListener('pointerdown', onPointer, true)
    for (const name of textEvents) box.removeEventListener(name, blockText, true)
  }
  const f = new FitAddon()
  t.loadAddon(f)
  term = t
  fit = f
  t.open(el)
  if (props.autofocus) t.focus()

  // Output is untrusted. What it could use to act on the page is swallowed:
  // titles and palette or colour changes. The clipboard (OSC 52) is only
  // ever set, shortly after the user's own input (takeOsc52), never read.
  for (const code of [0, 1, 2, 4, 10, 11, 12, 104, 110, 111, 112]) t.parser.registerOscHandler(code, () => true)
  t.parser.registerOscHandler(52, (data) => {
    takeOsc52(data)
    return true
  })

  if (t.textarea) {
    t.textarea.setAttribute('aria-label', props.label)
    t.textarea.setAttribute('aria-describedby', hintId)
  }
  t.attachCustomKeyEventHandler((ev) => {
    keyTurn()
    if (ev.type === 'keydown') lastInput = performance.now()
    if (ev.key === 'F6') {
      if (ev.type === 'keydown') {
        ev.preventDefault()
        leave(ev.shiftKey)
      }
      return false
    }
    const clip = clipboardKeyPressed(t, ev)
    if (clip !== undefined) return clip
    // Read-only: no key types into the terminal (copying and F6 above still work).
    if (locked.value) return false
    const lineBreak = lineBreakKey(ev)
    if (lineBreak !== null) {
      // xterm would send a plain CR (a submit); the keypress that follows is
      // swallowed as well.
      if (ev.type === 'keydown') {
        ev.preventDefault()
        copyPendingNow()
        sendInput(lineBreak)
      }
      return false
    }
    if (ev.type === 'keydown') copyPendingNow()
    return true
  })
  t.onData(sendInput)

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
    // Every connection replays the whole history: the screen starts empty for it.
    onReplayStart: () => t.reset(),
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
  clearTimeout(noteTimer)
  stopPointer?.()
  stopPointer = null
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
        <UiButton
          v-if="agentTerminal && state !== 'ended'"
          size="sm"
          class="tv__mode"
          :data-writable="writable ? '' : undefined"
          :title="writable ? 'Typing goes into the terminal. Switch back to read-only.' : 'Read-only. Switch on to type into the terminal, for single keys, arrow keys or Escape.'"
          data-terminal-mode
          @click="toggleWritable"
          >{{ writable ? 'Writable' : 'Read only' }}</UiButton
        >
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
    <div
      ref="screen"
      class="tv__screen"
      :data-readonly="locked ? '' : undefined"
      :data-size="sentSize || undefined"
      :data-drop="dropping ? '' : undefined"
      @pointerdown.capture="screenPress"
      @pointermove.capture="screenMove"
      @pointerup.capture="screenMove"
      @pointercancel="press = null"
      @click.capture="screenClick"
      @dragenter="onDragEnter"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop.capture="onDropEnd"
    >
      <div ref="host" class="tv__host" />
      <div v-if="dropping || note || pendingCopy !== null" class="tv__corner">
        <p v-if="dropping" class="tv__note" data-terminal-note>Drop images to paste their paths</p>
        <p v-else-if="note" class="tv__note" :class="{ 'tv__note--error': note.error }" data-terminal-note>{{ note.text }}</p>
        <UiButton
          v-if="pendingCopy !== null && !dropping"
          size="sm"
          title="The browser copies only on a click or key"
          data-terminal-copy
          @click="copyPendingClick"
          >Copy selection</UiButton
        >
      </div>
    </div>
    <TerminalDraft
      ref="draftBox"
      v-if="showDraft"
      :draft-id="draftId"
      :label="`Message to the ${label.charAt(0).toLowerCase()}${label.slice(1)}`"
      :send="sendDraft"
    />
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
      <template v-if="locked">Read only; use the message box below to write to the agent. </template><kbd>F6</kbd> leaves the terminal, <kbd>Shift+F6</kbd> goes to the element before it.
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
  position: relative;
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
  flex: 1 1 0;
  /* The message box and the hint below share the column: the screen may give way. */
  min-height: calc(var(--omo-space) * 30);
}
.tv--fill > .tv__screen > .tv__host {
  position: absolute;
  inset: calc(var(--omo-space) * 2) calc(var(--omo-space) * 2.5);
}
.tv__screen:focus-within,
.tv__screen[data-drop] {
  border-color: var(--omo-accent);
}
.tv__screen[data-drop] {
  outline: var(--omo-border) dashed var(--omo-accent);
  outline-offset: calc(var(--omo-space) * -1);
}
/* Notes and the Copy button sit over the top right corner of the screen. */
.tv__corner {
  position: absolute;
  top: var(--omo-space);
  right: var(--omo-space);
  z-index: 1;
  display: flex;
  align-items: center;
  gap: var(--omo-space);
  max-width: calc(100% - var(--omo-space) * 2);
}
.tv__note {
  margin: 0;
  padding: calc(var(--omo-space) * 0.5) calc(var(--omo-space) * 1.5);
  border: var(--omo-border) solid var(--omo-line);
  background: var(--omo-raised);
  color: var(--omo-fg);
  font-size: var(--omo-text-xs);
  line-height: var(--omo-text-xs-lh);
  overflow-wrap: anywhere;
}
.tv__note--error {
  color: var(--omo-danger);
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
