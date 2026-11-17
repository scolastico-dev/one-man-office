// Pasted text is often copied from what agents wrote (comments, output). It is
// sent as if typed, so control characters in it could act on the terminal or
// the shell: ESC[201~ ends bracketed paste early and the rest runs as typed
// input, Ctrl+C or Ctrl+D act on the program, DEL erases what was shown.

/**
 * Removes every C0 control character except tab, newline and carriage
 * return, as well as DEL and the C1 controls (which include the one-byte CSI).
 */
export function cleanPaste(text: string): string {
  return text.replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/g, '')
}

function filesOf(data: DataTransfer | null | undefined): File[] {
  try {
    return data?.files ? [...data.files] : []
  } catch {
    return []
  }
}

function textOf(data: DataTransfer | null | undefined): string {
  try {
    return data?.getData('text/plain') ?? ''
  } catch {
    return ''
  }
}

/**
 * Takes every paste and drop inside `el` before the emulator sees it, and
 * hands the cleaned text to `paste` instead. Covers the keyboard, the context
 * menu and the middle click (all of them are paste events) and dropped text.
 * Dropped files, and pasted files without text (an image copied in another
 * program), go to `files` instead. Returns a function that removes the
 * listeners.
 */
export function guardPaste(el: HTMLElement, paste: (text: string) => void, files: (list: File[]) => void): () => void {
  const take = (raw: string) => {
    const clean = cleanPaste(raw)
    if (clean !== '') paste(clean)
  }
  const onPaste = (ev: Event) => {
    ev.preventDefault()
    ev.stopPropagation()
    const data = (ev as ClipboardEvent).clipboardData
    const text = textOf(data)
    const list = text === '' ? filesOf(data) : []
    if (list.length > 0) files(list)
    else take(text)
  }
  const onDragOver = (ev: Event) => {
    ev.preventDefault()
  }
  const onDrop = (ev: Event) => {
    ev.preventDefault()
    ev.stopPropagation()
    const data = (ev as DragEvent).dataTransfer
    const list = filesOf(data)
    if (list.length > 0) files(list)
    else take(textOf(data))
  }
  el.addEventListener('paste', onPaste, true)
  el.addEventListener('dragover', onDragOver, true)
  el.addEventListener('drop', onDrop, true)
  return () => {
    el.removeEventListener('paste', onPaste, true)
    el.removeEventListener('dragover', onDragOver, true)
    el.removeEventListener('drop', onDrop, true)
  }
}
