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

/** Builds a line predicate from a user filter: regex (case-insensitive), or plain substring if it is not a valid regex. */
export function matcher(filter: string): (l: LogLine) => boolean {
  if (!filter) return () => true
  try {
    const re = new RegExp(filter, "i")
    return (l) => re.test(l.text) || re.test(l.service)
  } catch {
    const f = filter.toLowerCase()
    return (l) => l.text.toLowerCase().includes(f) || l.service.includes(f)
  }
}

export function filterLines(lines: readonly LogLine[], filter: string): readonly LogLine[] {
  return filter ? lines.filter(matcher(filter)) : lines
}

export function clock(ts: number): string {
  const d = new Date(ts)
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}:${String(d.getSeconds()).padStart(2, "0")}`
}

/** Plain text for copying/exporting: one line per entry, optionally with time and service prefix. */
export function formatLines(lines: readonly LogLine[], opts: { time?: boolean; prefix?: boolean } = {}): string {
  const width = opts.prefix ? Math.max(0, ...lines.map((l) => l.service.length)) : 0
  return lines
    .map((l) => {
      const head = [opts.time ? clock(l.ts) : "", opts.prefix ? `${l.service.padEnd(width)} │` : ""].filter(Boolean).join(" ")
      const text = l.stream === "system" ? `» ${l.text}` : l.text
      return head ? `${head} ${text}` : text
    })
    .join("\n")
}

/** Last `lines` lines of a file, reading at most the last `bytes` bytes. Empty if the file does not exist. */
export async function readTail(path: string, lines: number, bytes = 256 * 1024): Promise<string[]> {
  const file = Bun.file(path)
  if (!(await file.exists())) return []
  const size = file.size
  if (size === 0 || lines <= 0) return []
  const start = Math.max(0, size - bytes)
  let text = new TextDecoder().decode(await file.slice(start, size).bytes())
  // a cut in the middle of a line is not worth showing
  if (start > 0) text = text.slice(text.indexOf("\n") + 1)
  const out = text.split("\n")
  if (out.at(-1) === "") out.pop()
  return out.slice(-lines)
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
    this.add({ seq: 0, ts: Date.now(), service, stream, text: cleanLine(text) })
  }

  /** Adds a line produced elsewhere (a remote supervisor), keeping its time and text; the sequence number is ours. */
  ingest(line: LogLine) {
    this.add({ ...line })
  }

  private add(line: LogLine) {
    line.seq = ++this.seq
    const service = line.service
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

/**
 * Follows a log file that another process appends to (poll based, survives truncation).
 * Lets a service keep writing while orbit is closed and be re-read when it comes back.
 */
export class FileTail {
  private offset = 0
  private pending = ""
  private decoder = new TextDecoder()
  private timer?: ReturnType<typeof setInterval>
  private polling?: Promise<void>

  constructor(
    private path: string,
    private onLine: (line: string) => void,
    private interval = 50,
  ) {}

  /** `backlog`: start near the end of the file (last `backlogBytes`, at most `backlogLines` lines) instead of at 0. */
  async start(backlog?: { bytes: number; lines: number }) {
    const size = Bun.file(this.path).size
    if (backlog && size > 0) {
      for (const l of await readTail(this.path, backlog.lines, backlog.bytes)) this.onLine(l)
      this.offset = size
    }
    this.timer = setInterval(() => void this.poll(), this.interval)
  }

  poll(): Promise<void> {
    return (this.polling ??= this.read().finally(() => (this.polling = undefined)))
  }

  private async read() {
    const file = Bun.file(this.path)
    const size = file.size
    if (size < this.offset) this.offset = 0 // truncated
    if (size === this.offset) return
    const bytes = await file.slice(this.offset, size).bytes()
    this.offset = size
    this.pending += this.decoder.decode(bytes, { stream: true })
    let nl: number
    while ((nl = this.pending.indexOf("\n")) !== -1) {
      this.onLine(this.pending.slice(0, nl))
      this.pending = this.pending.slice(nl + 1)
    }
  }

  /** Stops following. `drain` reads what is left (including a last line without newline). */
  async stop(drain = true) {
    clearInterval(this.timer)
    if (!drain) return
    await this.polling
    await this.read()
    if (this.pending) this.onLine(this.pending)
    this.pending = ""
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
