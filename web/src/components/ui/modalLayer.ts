import { computed, shallowReactive } from 'vue'

/**
 * The modal dialogs that are open, the topmost last. A modal dialog makes
 * everything outside it inert, so whatever must stay usable while one is
 * open (the toasts and their actions) is rendered inside the topmost one.
 * Every modal dialog is opened and closed through these helpers.
 */
const open = shallowReactive<HTMLDialogElement[]>([])

/** The topmost open modal dialog, or null. */
export const topModal = computed<HTMLDialogElement | null>(() => open.at(-1) ?? null)

function forget(el: HTMLDialogElement) {
  const at = open.indexOf(el)
  if (at >= 0) open.splice(at, 1)
}

/** Opens el as a modal dialog. */
export function showModal(el: HTMLDialogElement) {
  el.showModal()
  forget(el)
  open.push(el)
  // Closed by the browser (a form with method="dialog", say).
  el.addEventListener('close', () => forget(el), { once: true })
}

/** Closes el, modal or not. */
export function closeDialog(el: HTMLDialogElement) {
  forget(el)
  el.close()
}
