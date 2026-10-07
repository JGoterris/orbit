import type { OrbitConfig } from "../../config/schema.ts"
import type { LogLine } from "../logs.ts"
import type { ServiceState } from "../supervisor.ts"

/** Bumped when a request or notification changes incompatibly. */
export const PROTOCOL = 2

/**
 * JSON-RPC 2.0, one message per line (NDJSON).
 *
 * requests (params → result)
 *   hello                                  → Hello
 *   snapshot                               → ServiceState[]
 *   logs {service?, limit?, sinceSeq?}     → LogLine[]
 *   start|stop|restart|toggle {services}   → {ok: boolean}   (groups are expanded)
 *   startAll | stopAll                     → {ok: true}
 *   toggleWatch {service}                  → {watch: "paused"|"active"|null}
 *   clearLogs {service?}                   → {ok: true}
 *   history {service, since?}              → ResourceBucket[]   cpu / memory in 10 s buckets, `since` = ms back
 *   subscribe {states?, logs?, services?}  → {ok: true, snapshot}  every notification after it is newer than the snapshot
 *   shutdown {how: "stop"|"detach"}        → {ok: true}      the server exits afterwards
 *
 * notifications (server → client): state {name, state}, log LogLine, cleared {service?}
 */
export interface Hello {
  protocol: number
  version: string
  pid: number
  stateDir: string
  config: OrbitConfig
}

export type Method =
  | "hello"
  | "snapshot"
  | "logs"
  | "start"
  | "stop"
  | "restart"
  | "toggle"
  | "startAll"
  | "stopAll"
  | "toggleWatch"
  | "clearLogs"
  | "history"
  | "subscribe"
  | "shutdown"

export interface Request {
  jsonrpc: "2.0"
  id: number
  method: Method
  params?: Record<string, unknown>
}

export interface Notification {
  jsonrpc: "2.0"
  method: "state" | "log" | "cleared"
  params: { name: string; state: ServiceState } | LogLine | { service?: string }
}

export interface Response {
  jsonrpc: "2.0"
  id: number | null
  result?: unknown
  error?: { code: number; message: string }
}

export const ERR = { parse: -32700, invalid: -32600, method: -32601, params: -32602, internal: -32603 } as const

export const encode = (msg: Request | Notification | Response) => JSON.stringify(msg) + "\n"

/** Splits a byte stream into JSON messages; lines that are not JSON are handed to `onBad`. */
/** Wire trace on stderr, for diagnosing a platform: set ORBIT_IPC_DEBUG=1. */
export function dbg(...args: unknown[]) {
  if (process.env.ORBIT_IPC_DEBUG) console.error(`[ipc ${Date.now() % 100000}]`, ...args)
}

export class LineParser {
  private buf = ""
  constructor(
    private onMessage: (msg: Record<string, unknown>) => void,
    private onBad: (line: string) => void = () => {},
  ) {}

  push(chunk: string) {
    this.buf += chunk
    let nl: number
    while ((nl = this.buf.indexOf("\n")) !== -1) {
      const line = this.buf.slice(0, nl).trim()
      this.buf = this.buf.slice(nl + 1)
      if (!line) continue
      try {
        const msg = JSON.parse(line)
        if (msg && typeof msg === "object" && !Array.isArray(msg)) this.onMessage(msg)
        else this.onBad(line)
      } catch {
        this.onBad(line)
      }
    }
  }
}
