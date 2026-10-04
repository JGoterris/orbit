import { EventEmitter } from "node:events"
import { findGitRoot } from "../git.ts"
import * as ops from "./ops.ts"
import { readStatus, type GitStatus } from "./status.ts"

/**
 * A git repository orbit watches: the last known status, branches, commits and stashes. Emits "change"
 * only when something actually changed, so polling does not re-render the UI for nothing.
 */
export class GitRepo extends EventEmitter {
  status?: GitStatus
  branches: ops.BranchInfo[] = []
  commits: ops.CommitInfo[] = []
  stashes: ops.StashInfo[] = []
  private pending?: Promise<void>
  private again?: "status" | "all"

  constructor(readonly root: string) {
    super()
    this.setMaxListeners(50)
  }

  static find(dir: string): GitRepo | undefined {
    const root = findGitRoot(dir)
    return root ? new GitRepo(root) : undefined
  }

  /** Refreshes the cheap part (status), or everything. Concurrent calls are coalesced into one more run. */
  refresh(what: "status" | "all" = "status"): Promise<void> {
    if (this.pending) {
      this.again = what === "all" || this.again === "all" ? "all" : "status"
      return this.pending
    }
    this.pending = this.load(what).finally(() => {
      this.pending = undefined
      const next = this.again
      this.again = undefined
      if (next) void this.refresh(next)
    })
    return this.pending
  }

  private async load(what: "status" | "all") {
    const [status, branches, commits, stashes] = await Promise.all([
      readStatus(this.root),
      what === "all" ? ops.branches(this.root) : undefined,
      what === "all" ? ops.log(this.root) : undefined,
      what === "all" ? ops.stashes(this.root) : undefined,
    ])
    let changed = false
    const set = (key: "status" | "branches" | "commits" | "stashes", value: unknown) => {
      if (value === undefined || JSON.stringify(this[key]) === JSON.stringify(value)) return
      Object.assign(this, { [key]: value })
      changed = true
    }
    set("status", status)
    set("branches", branches)
    set("commits", commits)
    set("stashes", stashes)
    if (changed) this.emit("change")
  }

  /** Runs a git operation and refreshes everything afterwards (also when it failed: it may have half-applied). */
  async run(op: () => Promise<ops.OpResult>): Promise<ops.OpResult> {
    const res = await op()
    await this.refresh("all")
    return res
  }
}
