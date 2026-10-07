import { readdirSync, readFileSync } from "node:fs"
import { homedir } from "node:os"
import { join } from "node:path"

/**
 * Everything that differs between Linux, macOS and Windows lives here: shells, process trees, pids,
 * process metrics, who owns a port and where per-user files go. The rest of orbit stays platform-agnostic.
 */
export const isWindows = process.platform === "win32"
export const isMac = process.platform === "darwin"
export const isLinux = process.platform === "linux"

// ── shells ──────────────────────────────────────────────────────────────────────────────────────

/** argv that runs `cmd` through a shell: `sh -c` on POSIX, `cmd.exe /d /s /c` on Windows, or the configured `shell`. */
export function shellArgv(cmd: string, shell?: string, win = isWindows): string[] {
  if (shell) {
    const name = shell.replace(/^.*[\\/]/, "").replace(/\.exe$/i, "").toLowerCase()
    if (name === "pwsh" || name === "powershell") return [shell, "-NoProfile", "-Command", cmd]
    if (name === "cmd") return [shell, "/d", "/s", "/c", cmd]
    return [shell, "-c", cmd]
  }
  return win ? [process.env.ComSpec || "cmd.exe", "/d", "/s", "/c", cmd] : ["/bin/sh", "-c", cmd]
}

/** The interactive shell of the user. */
export function userShell(): string {
  return isWindows ? process.env.ComSpec || "cmd.exe" : process.env.SHELL || "/bin/sh"
}

// ── pids and process trees ──────────────────────────────────────────────────────────────────────

export function pidAlive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch (err) {
    return (err as NodeJS.ErrnoException).code === "EPERM"
  }
}

/**
 * Signals a process and everything it spawned. POSIX: the whole process group (the process was started `detached`).
 * Windows has no signals nor groups: `taskkill /T` walks the tree, and only SIGKILL forces (`/F`).
 */
export function killTree(pid: number, signal: NodeJS.Signals): boolean {
  if (isWindows) {
    const args = ["/T", ...(signal === "SIGKILL" ? ["/F"] : []), "/PID", String(pid)]
    try {
      return Bun.spawnSync(["taskkill", ...args], { stdin: "ignore", stdout: "ignore", stderr: "ignore" }).exitCode === 0
    } catch {
      return false
    }
  }
  try {
    process.kill(-pid, signal)
    return true
  } catch {
    try {
      process.kill(pid, signal)
      return true
    } catch {
      return false
    }
  }
}

/** Is any process of the tree (group) still there? Windows cannot see orphans, so it is "the leader is alive". */
export function treeAlive(pid: number): boolean {
  if (isWindows) return pidAlive(pid)
  try {
    process.kill(-pid, 0)
    return true
  } catch {
    return false
  }
}

/**
 * Opaque start time of a pid, to tell a recycled pid from the process we launched: only compared for equality.
 * Undefined when the process is gone or the platform cannot say.
 */
export function procStartTime(pid: number): number | undefined {
  if (isLinux) {
    try {
      const stat = readFileSync(`/proc/${pid}/stat`, "utf8")
      // comm may contain spaces/parens: fields start after the last ')', starttime is field 22 (index 19 from field 3)
      const n = Number(stat.slice(stat.lastIndexOf(")") + 2).split(" ")[19])
      return Number.isFinite(n) ? n : undefined
    } catch {
      return undefined
    }
  }
  try {
    if (isMac) {
      const out = Bun.spawnSync(["ps", "-o", "lstart=", "-p", String(pid)], { stdin: "ignore", stderr: "ignore", env: { ...process.env, LC_ALL: "C" } })
      const ms = Date.parse(out.stdout.toString().trim())
      return out.exitCode === 0 && Number.isFinite(ms) ? ms : undefined
    }
    if (isWindows) {
      const script = `(Get-Process -Id ${Math.trunc(pid)} -ErrorAction Stop).StartTime.ToFileTimeUtc()`
      const out = Bun.spawnSync(["powershell", "-NoProfile", "-NonInteractive", "-Command", script], { stdin: "ignore", stderr: "ignore" })
      const n = Number(out.stdout.toString().trim())
      return out.exitCode === 0 && Number.isFinite(n) ? n : undefined
    }
  } catch {}
  return undefined
}

/** Querying the start time is cheap only on Linux; elsewhere it spawns a process, so pollers use `pidAlive` instead. */
export const cheapStartTime = isLinux

// ── metrics ─────────────────────────────────────────────────────────────────────────────────────

export interface ProcRow {
  ppid: number
  /** cumulative CPU time (user + system) in seconds */
  cpuSeconds: number
  /** resident memory in bytes */
  rss: number
}

const CLK_TCK = 100
const PAGE_SIZE = 4096

function linuxTable(): Map<number, ProcRow> {
  const table = new Map<number, ProcRow>()
  let entries: string[]
  try {
    entries = readdirSync("/proc")
  } catch {
    return table
  }
  for (const entry of entries) {
    const pid = Number(entry)
    if (!Number.isInteger(pid)) continue
    try {
      const stat = readFileSync(`/proc/${pid}/stat`, "utf8")
      const fields = stat.slice(stat.lastIndexOf(")") + 2).split(" ")
      table.set(pid, {
        ppid: Number(fields[1]),
        cpuSeconds: (Number(fields[11]) + Number(fields[12])) / CLK_TCK,
        rss: Number(fields[21]) * PAGE_SIZE,
      })
    } catch {
      // process vanished
    }
  }
  return table
}

/** `ps` CPU time: `[dd-][hh:]mm:ss[.ss]` to seconds. */
export function parsePsTime(text: string): number {
  const m = /^(?:(\d+)-)?(?:(\d+):)?(\d+):(\d+(?:\.\d+)?)$/.exec(text.trim())
  if (!m) return 0
  return Number(m[1] ?? 0) * 86400 + Number(m[2] ?? 0) * 3600 + Number(m[3]) * 60 + Number(m[4])
}

export function parsePsTable(text: string): Map<number, ProcRow> {
  const table = new Map<number, ProcRow>()
  for (const line of text.split("\n")) {
    const [pid, ppid, rssKb, time] = line.trim().split(/\s+/)
    if (!time || !Number.isInteger(Number(pid))) continue
    table.set(Number(pid), { ppid: Number(ppid), cpuSeconds: parsePsTime(time), rss: Number(rssKb) * 1024 })
  }
  return table
}

/** Win32_Process rows (KernelModeTime / UserModeTime are in 100 ns units). */
export function parseWinTable(json: string): Map<number, ProcRow> {
  const table = new Map<number, ProcRow>()
  let rows: unknown
  try {
    rows = JSON.parse(json)
  } catch {
    return table
  }
  for (const r of Array.isArray(rows) ? rows : [rows]) {
    const row = r as { ProcessId?: number; ParentProcessId?: number; WorkingSetSize?: number; KernelModeTime?: number | string; UserModeTime?: number | string }
    if (typeof row?.ProcessId !== "number") continue
    table.set(row.ProcessId, {
      ppid: Number(row.ParentProcessId ?? 0),
      cpuSeconds: (Number(row.KernelModeTime ?? 0) + Number(row.UserModeTime ?? 0)) / 1e7,
      rss: Number(row.WorkingSetSize ?? 0),
    })
  }
  return table
}

async function run(argv: string[], timeout: number): Promise<string> {
  try {
    const proc = Bun.spawn(argv, { stdin: "ignore", stdout: "pipe", stderr: "ignore", env: { ...process.env, LC_ALL: "C" } })
    const timer = setTimeout(() => proc.kill(), timeout)
    const out = await new Response(proc.stdout).text()
    clearTimeout(timer)
    return (await proc.exited) === 0 ? out : ""
  } catch {
    return ""
  }
}

/** Snapshot of every process: pid -> (ppid, cpu seconds, rss bytes). */
export async function processTable(): Promise<Map<number, ProcRow>> {
  if (isLinux) return linuxTable()
  if (isMac) return parsePsTable(await run(["ps", "-axo", "pid=,ppid=,rss=,time="], 5000))
  if (isWindows) {
    const script =
      "Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,WorkingSetSize,KernelModeTime,UserModeTime | ConvertTo-Json -Compress"
    return parseWinTable(await run(["powershell", "-NoProfile", "-NonInteractive", "-Command", script], 10_000))
  }
  return new Map()
}

// ── ports ───────────────────────────────────────────────────────────────────────────────────────

/** Best effort: which process listens on a TCP port. */
export async function whoListens(port: number): Promise<string | undefined> {
  if (isMac) {
    const out = await run(["lsof", "-nP", `-iTCP:${port}`, "-sTCP:LISTEN", "-Fpc"], 3000)
    const pid = /^p(\d+)/m.exec(out)?.[1]
    const name = /^c(.+)$/m.exec(out)?.[1]
    return pid ? `${name ?? "process"} (pid ${pid})` : undefined
  }
  if (isWindows) {
    const out = await run(["netstat", "-ano", "-p", "tcp"], 5000)
    const re = new RegExp(`^\\s*TCP\\s+\\S+:${port}\\s+\\S+\\s+LISTENING\\s+(\\d+)`, "im")
    const pid = re.exec(out)?.[1]
    if (!pid) return undefined
    const task = await run(["tasklist", "/FI", `PID eq ${pid}`, "/FO", "CSV", "/NH"], 3000)
    const name = /^"([^"]+)"/.exec(task.trim())?.[1]
    return `${name ?? "process"} (pid ${pid})`
  }
  const out = await run(["ss", "-ltnpH", `sport = :${port}`], 2000)
  const m = /users:\(\("([^"]+)",pid=(\d+)/.exec(out)
  return m ? `${m[1]} (pid ${m[2]})` : out.trim() ? "another process" : undefined
}

// ── per-user directories ────────────────────────────────────────────────────────────────────────

/** State (logs, pids, sockets): $XDG_STATE_HOME, %LOCALAPPDATA% on Windows, ~/.local/state elsewhere. */
export function stateBase(): string {
  if (process.env.XDG_STATE_HOME) return process.env.XDG_STATE_HOME
  if (isWindows) return process.env.LOCALAPPDATA || join(homedir(), "AppData", "Local")
  return join(homedir(), ".local", "state")
}

/** Per-user settings: $XDG_CONFIG_HOME, %APPDATA% on Windows, ~/.config elsewhere. */
export function configBase(): string {
  if (process.env.XDG_CONFIG_HOME) return process.env.XDG_CONFIG_HOME
  if (isWindows) return process.env.APPDATA || join(homedir(), "AppData", "Roaming")
  return join(homedir(), ".config")
}

// ── pty ─────────────────────────────────────────────────────────────────────────────────────────

/** Can `Bun.Terminal` run a console here? */
export const ptySupported = typeof (Bun as { Terminal?: unknown }).Terminal === "function"
