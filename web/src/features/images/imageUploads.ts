// Uploading images into a Markdown text box (the comment composer, the
// description editor): from the "Add image…" picker, a paste or a drop. Each
// file gets a placeholder line at the caret while it uploads; on success the
// placeholder becomes the server's Markdown reference, on a refusal it goes
// away and the server's sentence is shown next to the box. Nothing else is
// inserted. The server decides from the bytes what is an image; only the
// size limit is also checked here, so a large file is not sent for nothing.
import { ref, type Ref } from 'vue'
import { ApiError } from '../../api/client'
import { failureText } from '../../api/lostAnswer'
import type { Image } from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'

/** The server's limit for one image, in bytes. */
export const MAX_IMAGE_BYTES = 10 * 1024 * 1024
/** What the file picker offers; the server still checks the bytes. */
export const IMAGE_ACCEPT = 'image/png,image/jpeg,image/gif,image/webp'

/** Uploads one file to the ticket (`uploadImage` of the endpoints). */
export type UploadImage = (file: Blob, name: string) => Promise<Image>

export interface ImageUploadDeps {
  upload: UploadImage
  /** The text of the box; the upload edits it. */
  text: Ref<string>
  /** The text box, for the caret. */
  area: () => HTMLTextAreaElement | null
  announce?: (message: string, urgent: boolean) => void
}

export interface ImageUploads {
  /** Uploads files one after another into the text at the caret. */
  add(files: readonly File[]): Promise<void>
  /** Refusals of the last uploads, one sentence each ("shot.svg: …"). */
  errors: Ref<string[]>
  /** How many uploads are under way. */
  busy: Ref<number>
  /** The image files of a paste or drop (none when it carries no file). */
  filesOf(data: DataTransfer | null): File[]
  /** Handlers for the text box: a paste or drop of image files uploads them instead of inserting text. */
  onPaste(e: ClipboardEvent): void
  onDragOver(e: DragEvent): void
  onDrop(e: DragEvent): void
  clearErrors(): void
}

let nextPlaceholder = 0

/** The placeholder line of an upload: it renders as text, never as an image. */
function placeholderFor(name: string): string {
  const shown = visibleText(name).replace(/[[\]\\]/g, '')
  return `![Uploading ${shown}…](#upload-${++nextPlaceholder})`
}

/** Inserts line on a line of its own at the caret; returns all that was inserted. */
function insertAtCaret(d: ImageUploadDeps, line: string): string {
  const el = d.area()
  const text = d.text.value
  const at = el && document.activeElement === el ? el.selectionStart : text.length
  const before = text.slice(0, at)
  const after = text.slice(at)
  const lead = before === '' || before.endsWith('\n') ? '' : '\n'
  const tail = after.startsWith('\n') ? '' : '\n'
  d.text.value = `${before}${lead}${line}${tail}${after}`
  const caret = before.length + lead.length + line.length + tail.length
  if (el && document.activeElement === el) {
    queueMicrotask(() => el.setSelectionRange(caret, caret))
  }
  return lead + line + tail
}

function replace(d: ImageUploads & { deps: ImageUploadDeps }, placeholder: string, by: string | null, inserted: string) {
  const text = d.deps.text.value
  if (by === null && text.includes(inserted)) {
    // Removed with the line breaks it added.
    d.deps.text.value = text.replace(inserted, '')
    return
  }
  const i = text.indexOf(placeholder)
  if (i < 0) {
    // The placeholder was edited away; a reference still goes at the end.
    if (by !== null) d.deps.text.value = text === '' || text.endsWith('\n') ? `${text}${by}` : `${text}\n${by}`
    return
  }
  if (by !== null) {
    d.deps.text.value = text.slice(0, i) + by + text.slice(i + placeholder.length)
    return
  }
  // Removed with the line break it added.
  const end = text.charAt(i + placeholder.length) === '\n' ? i + placeholder.length + 1 : i + placeholder.length
  d.deps.text.value = text.slice(0, i) + text.slice(end)
}

function refusal(name: string, e: unknown): string {
  const why = failureText(e, {
    lost: 'no answer from omo; it may have been stored, look before you add it again',
    refused: 'omo refused it',
  })
  return `${visibleText(name)}: ${e instanceof ApiError ? e.message : why}`
}

export function createImageUploads(deps: ImageUploadDeps): ImageUploads {
  const errors = ref<string[]>([])
  const busy = ref(0)
  const self = {
    deps,
    errors,
    busy,
    clearErrors() {
      errors.value = []
    },
    filesOf(data: DataTransfer | null): File[] {
      if (!data) return []
      return [...data.files].filter((f) => f.type === '' || f.type.startsWith('image/'))
    },
    onPaste(e: ClipboardEvent) {
      const files = self.filesOf(e.clipboardData)
      if (files.length === 0) return
      e.preventDefault()
      void self.add(files)
    },
    onDragOver(e: DragEvent) {
      if (e.dataTransfer?.types.includes('Files')) {
        e.preventDefault()
        e.dataTransfer.dropEffect = 'copy'
      }
    },
    onDrop(e: DragEvent) {
      const files = self.filesOf(e.dataTransfer)
      if (files.length === 0) return
      e.preventDefault()
      ;(e.target as HTMLElement | null)?.focus?.()
      void self.add(files)
    },
    async add(files: readonly File[]) {
      if (files.length === 0) return
      errors.value = []
      for (const file of files) {
        const name = file.name || 'pasted image.png'
        if (file.size > MAX_IMAGE_BYTES) {
          errors.value = [...errors.value, `${visibleText(name)}: an image has at most 10 MiB; this one has ${(file.size / 1048576).toFixed(1)} MiB`]
          continue
        }
        const placeholder = placeholderFor(name)
        const inserted = insertAtCaret(deps, placeholder)
        busy.value++
        deps.announce?.(`Uploading ${visibleText(name)}.`, false)
        try {
          const image = await deps.upload(file, name)
          replace(self, placeholder, image.markdown, inserted)
          deps.announce?.(`${visibleText(name)} added.`, false)
        } catch (e) {
          replace(self, placeholder, null, inserted)
          errors.value = [...errors.value, refusal(name, e)]
        } finally {
          busy.value--
        }
      }
      if (errors.value.length > 0) deps.announce?.(errors.value.join(' '), true)
    },
  }
  return self
}
