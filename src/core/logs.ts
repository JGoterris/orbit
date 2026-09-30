export type LogStream = "stdout" | "stderr" | "system"

export interface LogLine {
  seq: number
  ts: number
  service: string
  stream: LogStream
  text: string
}

/** Fixed-size FIFO buffer. */
export class Ring<T> {
  private items: T[] = []
  constructor(public readonly capacity: number) {}
  push(item: T) {
    this.items.push(item)
    // trim in chunks so we don't splice on every push
    if (this.items.length > this.capacity * 1.25) this.items = this.items.slice(-this.capacity)
  }
  toArray(): readonly T[] {
    return this.items.length > this.capacity ? this.items.slice(-this.capacity) : this.items
  }
  get length() {
    return Math.min(this.items.length, this.capacity)
  }
  clear() {
    this.items = []
  }
}

// eslint-disable-next-line no-control-regex
const ANSI_RE = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]/g

export function stripAnsi(s: string): string {
  return s.replace(ANSI_RE, "")
}

/** Cleans a raw line: ANSI, carriage-return progress bars, tabs. */
export function cleanLine(raw: string): string {
  let s = stripAnsi(raw)
  const cr = s.lastIndexOf("\r")
  if (cr !== -1) s = s.slice(cr + 1)
  return s.replace(/\t/g, "  ").replace(/[\x00-\x08\x0b-\x1f\x7f]/g, "")
}

export type LogLevel = "error" | "warn" | "info" | "debug" | undefined

export function detectLevel(text: string): LogLevel {
  if (/\b(error|err|fatal|panic|exception|failed|traceback)\b/i.test(text)) return "error"
  if (/\b(warn|warning|deprecated)\b/i.test(text)) return "warn"
  if (/\b(debug|trace|verbose)\b/i.test(text)) return "debug"
  return undefined
}

export class LogStore {
  private seq = 0
  readonly all: Ring<LogLine>
  private perService = new Map<string, Ring<LogLine>>()
  private listeners = new Set<(line: LogLine) => void>()

  constructor(
    private perServiceCapacity = 5000,
    allCapacity = 20000,
  ) {
    this.all = new Ring(allCapacity)
  }

  append(service: string, stream: LogStream, text: string) {
    const line: LogLine = { seq: ++this.seq, ts: Date.now(), service, stream, text: cleanLine(text) }
    this.all.push(line)
    let ring = this.perService.get(service)
    if (!ring) this.perService.set(service, (ring = new Ring(this.perServiceCapacity)))
    ring.push(line)
    for (const l of this.listeners) l(line)
  }

  lines(service?: string): readonly LogLine[] {
    if (!service) return this.all.toArray()
    return this.perService.get(service)?.toArray() ?? []
  }

  clear(service?: string) {
    if (!service) {
      this.all.clear()
      this.perService.clear()
    } else {
      this.perService.get(service)?.clear()
    }
    this.seq++
  }

  get version() {
    return this.seq
  }

  onLine(fn: (line: LogLine) => void): () => void {
    this.listeners.add(fn)
    return () => this.listeners.delete(fn)
  }
}

/** Splits a byte stream into lines and calls `onLine` for each. */
export async function pipeLines(
  stream: ReadableStream<Uint8Array> | null | undefined,
  onLine: (line: string) => void,
): Promise<void> {
  if (!stream) return
  const decoder = new TextDecoder()
  let buffer = ""
  try {
    for await (const chunk of stream) {
      buffer += decoder.decode(chunk, { stream: true })
      let nl: number
      while ((nl = buffer.indexOf("\n")) !== -1) {
        onLine(buffer.slice(0, nl))
        buffer = buffer.slice(nl + 1)
      }
      // guard against output without newlines growing forever
      if (buffer.length > 16_384) {
        onLine(buffer)
        buffer = ""
      }
    }
  } catch {
    // stream closed abruptly
  }
  buffer += decoder.decode()
  if (buffer.length) onLine(buffer)
}
