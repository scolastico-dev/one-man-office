import { reactive, readonly } from 'vue'

export type ToastTone = 'info' | 'error' | 'warn'

export interface ToastInput {
  title: string
  message?: string
  tone?: ToastTone
  action?: { label: string; run(): void }
  /** How long the toast stays; 0 keeps it until dismissed. */
  timeoutMs?: number
}

export interface Toast extends Required<Pick<ToastInput, 'title' | 'tone'>> {
  id: number
  message?: string
  action?: ToastInput['action']
}

const DEFAULT_TIMEOUT: Record<ToastTone, number> = { info: 5000, warn: 8000, error: 8000 }
const MAX_TOASTS = 4

const toasts = reactive<Toast[]>([])
const timers = new Map<number, { handle?: ReturnType<typeof setTimeout>; remaining: number; started: number }>()
let nextId = 1

function arm(id: number) {
  const timer = timers.get(id)
  if (!timer || timer.remaining <= 0) return
  timer.started = Date.now()
  timer.handle = setTimeout(() => dismiss(id), timer.remaining)
}

function dismiss(id: number): void {
  const timer = timers.get(id)
  if (timer?.handle) clearTimeout(timer.handle)
  timers.delete(id)
  const index = toasts.findIndex((t) => t.id === id)
  if (index >= 0) toasts.splice(index, 1)
}

function show(input: ToastInput): number {
  const id = nextId++
  const tone = input.tone ?? 'info'
  toasts.push({ id, tone, title: input.title, message: input.message, action: input.action })
  while (toasts.length > MAX_TOASTS) dismiss(toasts[0]!.id)
  timers.set(id, { remaining: input.timeoutMs ?? DEFAULT_TIMEOUT[tone], started: 0 })
  arm(id)
  return id
}

/** Stops the countdown while the pointer or focus is on the toast. */
export function pauseToast(id: number): void {
  const timer = timers.get(id)
  if (!timer?.handle) return
  clearTimeout(timer.handle)
  timer.handle = undefined
  timer.remaining -= Date.now() - timer.started
}

export function resumeToast(id: number): void {
  const timer = timers.get(id)
  if (timer && !timer.handle) arm(id)
}

/** The toasts on screen, oldest first; rendered by Toaster. */
export const activeToasts = readonly(toasts)

export function useToast() {
  return { show, dismiss }
}
