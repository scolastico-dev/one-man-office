/** One complete server-sent event: its blank line has arrived. */
export interface SseFrame {
  /** The frame's own `id:` field, or null when the frame had none. */
  id: string | null
  /** The `event:` field, `message` when absent. */
  event: string
  /** The `data:` lines joined with `\n`. */
  data: string
}

const lineBreak = /[\r\n]/g

/**
 * Incremental parser for a `text/event-stream` body. `push` takes decoded
 * text in chunks of any size and returns the frames completed by it.
 *
 * Differences from the browser's EventSource: a frame's id is reported only
 * on that frame (it is not inherited by later frames), so a frame without an
 * `id:` line, such as omo's resync frame, has `id: null`. Frames without any
 * `data:` line dispatch nothing, as in EventSource.
 */
export class SseParser {
  /** The last valid `retry:` value in milliseconds, null until one arrived. */
  retry: number | null = null
  /** How many comment lines (keep-alive pings) were read. */
  comments = 0

  private started = false
  private skipLF = false
  private partial = ''
  private id: string | null = null
  private event = ''
  private data: string[] = []
  private hasData = false

  push(text: string): SseFrame[] {
    const out: SseFrame[] = []
    let i = 0
    if (!this.started && text.length > 0) {
      this.started = true
      if (text.charCodeAt(0) === 0xfeff) i = 1
    }
    if (this.skipLF && i < text.length) {
      // The previous chunk ended with CR; a LF right after it belongs to the
      // same line end.
      if (text.charCodeAt(i) === 10) i++
      this.skipLF = false
    }
    while (i < text.length) {
      lineBreak.lastIndex = i
      const m = lineBreak.exec(text)
      if (m === null) {
        this.partial += text.slice(i)
        break
      }
      let j = m.index
      const line = this.partial + text.slice(i, j)
      this.partial = ''
      if (text.charCodeAt(j) === 13) {
        if (j + 1 < text.length) {
          if (text.charCodeAt(j + 1) === 10) j++
        } else {
          this.skipLF = true
        }
      }
      i = j + 1
      this.line(line, out)
    }
    return out
  }

  private line(line: string, out: SseFrame[]): void {
    if (line === '') {
      if (this.hasData) out.push({ id: this.id, event: this.event || 'message', data: this.data.join('\n') })
      this.id = null
      this.event = ''
      this.data = []
      this.hasData = false
      return
    }
    if (line.charCodeAt(0) === 58) {
      this.comments++
      return
    }
    const colon = line.indexOf(':')
    let field = line
    let value = ''
    if (colon >= 0) {
      field = line.slice(0, colon)
      value = line.slice(colon + 1)
      if (value.charCodeAt(0) === 32) value = value.slice(1)
    }
    switch (field) {
      case 'event':
        this.event = value
        break
      case 'data':
        this.data.push(value)
        this.hasData = true
        break
      case 'id':
        if (!value.includes('\0')) this.id = value
        break
      case 'retry':
        if (/^\d+$/.test(value)) this.retry = Number(value)
        break
      default:
      // Unknown fields are ignored.
    }
  }
}
