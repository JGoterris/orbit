import type { LogLine } from "./logs.ts"

export type TraceKind = "java" | "node" | "python"

/** Set on the first line of a stack trace (the "head"): what kind it is and how many lines follow it. */
export interface TraceInfo {
  kind: TraceKind
  frames: number
  /** python: the closing `ValueError: …` line, the only informative one once the trace is folded */
  summary?: string
}

const FRAME = /^\s+at \S/
const JAVA_FRAME = /^\s+at [\w$.<>/]+\(.*\)/
const JAVA_MORE = /^\s+\.\.\. \d+ (?:more|common frames omitted)/
const JAVA_EXTRA = /^(?:Caused by: |\s+Suppressed: )/
const PY_HEAD = /^Traceback \(most recent call last\):/
const PY_CHAIN = /^(?:During handling of the above exception|The above exception was the direct cause)/
const PY_FINAL = /^[A-Za-z_][\w.]*(?:Error|Exception|Exit|Interrupt|Warning|Failure)(?::\s.*)?$|^[A-Za-z_][\w.]*: .*/

interface State {
  /** previous line of this stream: the head of a Java/Node trace is only known once its first frame arrives */
  last?: LogLine
  head?: LogLine
  /** python: the closing exception line was seen (only chaining text may follow) */
  final: boolean
}

/**
 * Finds Java / Node / Python stack traces in a line stream as it arrives and tags the lines
 * (`trace` on the head, `traceOf` on the rest). State is per service and stream, so interleaved output does not mix.
 */
export class TraceDetector {
  private states = new Map<string, State>()

  feed(line: LogLine) {
    const key = `${line.service}\0${line.stream}`
    let s = this.states.get(key)
    if (!s) this.states.set(key, (s = { final: false }))
    if (line.stream === "system") {
      s.head = s.last = undefined
      return
    }
    const text = line.text
    if (s.head && this.continues(s, s.head, text)) {
      this.attach(s.head, line)
    } else {
      s.head = undefined
      if (PY_HEAD.test(text)) {
        line.trace = { kind: "python", frames: 0 }
        s.head = line
        s.final = false
      } else if (FRAME.test(text) && s.last && !s.last.trace && s.last.traceOf === undefined) {
        s.head = s.last
        s.head.trace = { kind: JAVA_FRAME.test(text) ? "java" : "node", frames: 0 }
        this.attach(s.head, line)
      }
    }
    s.last = line
  }

  /** Forgets the open traces of a service (or of all of them). */
  reset(service?: string) {
    if (!service) return this.states.clear()
    for (const key of this.states.keys()) if (key.startsWith(`${service}\0`)) this.states.delete(key)
  }

  private attach(head: LogLine, line: LogLine) {
    line.traceOf = head.seq
    head.trace!.frames++
  }

  private continues(s: State, head: LogLine, text: string): boolean {
    const trace = head.trace!
    if (trace.kind !== "python") return FRAME.test(text) || JAVA_MORE.test(text) || JAVA_EXTRA.test(text)
    if (text.trim() !== "" && /^\s/.test(text)) return !s.final
    if (PY_HEAD.test(text)) {
      s.final = false
      return true
    }
    if (text === "" || PY_CHAIN.test(text)) return s.final
    if (!s.final && trace.frames > 0 && PY_FINAL.test(text)) {
      s.final = true
      trace.summary = text
      return true
    }
    return false
  }
}
