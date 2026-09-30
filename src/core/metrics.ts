import { readdirSync, readFileSync } from "node:fs"
import { exec } from "./exec.ts"

export interface Sample {
  cpu: number // percent of one core
  mem: number // bytes
}

const CLK_TCK = 100
const PAGE_SIZE = 4096

interface ProcStat {
  ppid: number
  ticks: number
  rss: number
}

/** Snapshot of /proc: pid -> (ppid, cpu ticks, rss bytes). Linux only. */
function readProcTable(): Map<number, ProcStat> {
  const table = new Map<number, ProcStat>()
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
      // comm may contain spaces/parens: fields start after the last ')'
      const fields = stat.slice(stat.lastIndexOf(")") + 2).split(" ")
      table.set(pid, {
        ppid: Number(fields[1]),
        ticks: Number(fields[11]) + Number(fields[12]),
        rss: Number(fields[21]) * PAGE_SIZE,
      })
    } catch {
      // process vanished
    }
  }
  return table
}

/** CPU%/RSS for whole process trees, computed from successive /proc snapshots. */
export class ProcessSampler {
  private last = new Map<number, { ticks: number; at: number }>()

  sample(roots: number[]): Map<number, Sample> {
    const out = new Map<number, Sample>()
    if (process.platform !== "linux" || roots.length === 0) return out
    const table = readProcTable()
    const children = new Map<number, number[]>()
    for (const [pid, s] of table) {
      const list = children.get(s.ppid)
      if (list) list.push(pid)
      else children.set(s.ppid, [pid])
    }
    const now = performance.now()
    for (const root of roots) {
      if (!table.has(root)) continue
      let ticks = 0
      let rss = 0
      const stack = [root]
      while (stack.length) {
        const pid = stack.pop()!
        const s = table.get(pid)
        if (!s) continue
        ticks += s.ticks
        rss += s.rss
        stack.push(...(children.get(pid) ?? []))
      }
      const prev = this.last.get(root)
      const cpu = prev ? Math.max(0, ((ticks - prev.ticks) / CLK_TCK / ((now - prev.at) / 1000)) * 100) : 0
      this.last.set(root, { ticks, at: now })
      out.set(root, { cpu, mem: rss })
    }
    for (const pid of this.last.keys()) if (!table.has(pid)) this.last.delete(pid)
    return out
  }
}

const UNITS: Record<string, number> = {
  b: 1,
  kb: 1e3,
  kib: 1024,
  mb: 1e6,
  mib: 1024 ** 2,
  gb: 1e9,
  gib: 1024 ** 3,
}

export function parseSize(s: string): number {
  const m = /([\d.]+)\s*([a-z]+)/i.exec(s.trim())
  if (!m) return 0
  return Number(m[1]) * (UNITS[m[2]!.toLowerCase()] ?? 1)
}

/** One `docker stats` call for many containers. Keys are the ids passed in (prefix match). */
export async function sampleContainers(ids: string[]): Promise<Map<string, Sample>> {
  const out = new Map<string, Sample>()
  if (ids.length === 0) return out
  const res = await exec(["docker", "stats", "--no-stream", "--format", "{{json .}}", ...ids], { timeout: 15_000 })
  for (const line of res.stdout.split("\n")) {
    if (!line.trim()) continue
    try {
      const row = JSON.parse(line) as { ID: string; CPUPerc: string; MemUsage: string }
      const id = ids.find((i) => i.startsWith(row.ID) || row.ID.startsWith(i))
      if (!id) continue
      out.set(id, { cpu: Number.parseFloat(row.CPUPerc) || 0, mem: parseSize(row.MemUsage.split("/")[0] ?? "") })
    } catch {
      // ignore malformed rows
    }
  }
  return out
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n.toFixed(0)}B`
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(0)}K`
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(n < 10 * 1024 ** 2 ? 1 : 0)}M`
  return `${(n / 1024 ** 3).toFixed(1)}G`
}

export function formatDuration(ms: number): string {
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m${String(s % 60).padStart(2, "0")}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h${String(m % 60).padStart(2, "0")}m`
  return `${Math.floor(h / 24)}d${h % 24}h`
}
