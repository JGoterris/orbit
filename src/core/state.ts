import { createHash } from "node:crypto"
import { mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"
import type { OrbitConfig } from "../config/schema.ts"

/** What orbit remembers about a service it launched, so a later session can pick it up again. */
export interface SavedService {
  /** process services: pid of the group leader */
  pid?: number
  /** process services: start time of `pid` (guards against pid reuse) */
  startTime?: number
  startedAt: number
}

export interface SavedState {
  services: Record<string, SavedService>
}

/** Per-project directory for logs, pids and the lock: ~/.local/state/orbit/<name>-<hash of root>. */
export function stateDir(config: OrbitConfig): string {
  const base = process.env.XDG_STATE_HOME || join(homedir(), ".local", "state")
  const hash = createHash("sha1").update(config.root).digest("hex").slice(0, 8)
  return join(base, "orbit", `${config.name}-${hash}`.replace(/[^a-zA-Z0-9_.-]/g, "-"))
}

export interface ProcFiles {
  out: string
  err: string
  exit: string
}

export function procFiles(dir: string, service: string): ProcFiles {
  const base = join(dir, "logs", service.replace(/[^a-zA-Z0-9_.-]/g, "-"))
  return { out: `${base}.out.log`, err: `${base}.err.log`, exit: `${base}.exit` }
}

export function readState(dir: string): SavedState {
  try {
    const raw = JSON.parse(readFileSync(join(dir, "state.json"), "utf8")) as Partial<SavedState>
    return { services: raw.services ?? {} }
  } catch {
    return { services: {} }
  }
}

export function writeState(dir: string, state: SavedState) {
  try {
    mkdirSync(dir, { recursive: true })
    const tmp = join(dir, `state.json.${process.pid}.tmp`)
    writeFileSync(tmp, JSON.stringify(state, null, 2))
    renameSync(tmp, join(dir, "state.json"))
  } catch {
    // state is best effort: orbit keeps working without it
  }
}

/** Start time of a pid in clock ticks since boot (Linux), undefined if it is gone or /proc is missing. */
export function procStartTime(pid: number): number | undefined {
  try {
    const stat = readFileSync(`/proc/${pid}/stat`, "utf8")
    // comm may contain spaces/parens: fields start after the last ')', starttime is field 22 (index 19 from field 3)
    const n = Number(stat.slice(stat.lastIndexOf(")") + 2).split(" ")[19])
    return Number.isFinite(n) ? n : undefined
  } catch {
    return undefined
  }
}

function pidAlive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === "EPERM"
  }
}

/** Takes the per-project lock. Returns the pid of the orbit that already holds it, if any. */
export function acquireLock(dir: string): number | undefined {
  const file = join(dir, "orbit.lock")
  const holder = readLock(dir)
  if (holder && holder !== process.pid && pidAlive(holder)) return holder
  try {
    mkdirSync(dir, { recursive: true })
    writeFileSync(file, String(process.pid))
  } catch {}
  return undefined
}

export function readLock(dir: string): number | undefined {
  try {
    const pid = Number.parseInt(readFileSync(join(dir, "orbit.lock"), "utf8"), 10)
    return Number.isInteger(pid) && pidAlive(pid) ? pid : undefined
  } catch {
    return undefined
  }
}

export function releaseLock(dir: string) {
  if (readLock(dir) === process.pid) rmSync(join(dir, "orbit.lock"), { force: true })
}
