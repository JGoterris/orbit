import { EventEmitter } from "node:events"

/** One git command orbit ran on the user's behalf (not the polling reads: status, log, branches, diffs). */
export interface CmdEntry {
  /** repo it ran in */
  root: string
  /** arguments after `git` */
  args: string[]
  ok: boolean
  /** git's own first useful line, empty when it had nothing to say */
  message: string
  at: Date
  ms: number
}

const MAX = 300

/** What the Git view's Commands panel shows; filled by the operations in ops.ts. */
class CommandLog extends EventEmitter {
  entries: CmdEntry[] = []

  add(entry: CmdEntry) {
    this.entries.push(entry)
    if (this.entries.length > MAX) this.entries.splice(0, this.entries.length - MAX)
    this.emit("change")
  }

  clear() {
    this.entries = []
    this.emit("change")
  }
}

export const cmdLog = new CommandLog()
cmdLog.setMaxListeners(50)

/** `git commit -F -` as the user would read it: arguments with spaces are quoted. */
export const formatArgs = (args: string[]) => args.map((a) => (/[\s"']/.test(a) ? JSON.stringify(a) : a)).join(" ")
