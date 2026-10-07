import { totalmem } from "node:os"
import { exec } from "./exec.ts"
import { processTable } from "./platform/index.ts"

export interface Sample {
  cpu: number // percent of one core: a multithreaded service can pass 100
  mem: number // bytes
  /** memory limit of the container, when it has one */
  limit?: number
}

/** CPU%/RSS for whole process trees, computed from successive process-table snapshots. */
export class ProcessSampler {
  private last = new Map<number, { cpuSeconds: number; at: number }>()

  async sample(roots: number[]): Promise<Map<number, Sample>> {
    const out = new Map<number, Sample>()
    if (roots.length === 0) return out
    const table = await processTable()
    const children = new Map<number, number[]>()
    for (const [pid, s] of table) {
      const list = children.get(s.ppid)
      if (list) list.push(pid)
      else children.set(s.ppid, [pid])
    }
    const now = performance.now()
    for (const root of roots) {
      if (!table.has(root)) continue
      let cpuSeconds = 0
      let rss = 0
      const seen = new Set<number>()
      const stack = [root]
      while (stack.length) {
        const pid = stack.pop()!
        const s = table.get(pid)
        if (!s || seen.has(pid)) continue
        seen.add(pid)
        cpuSeconds += s.cpuSeconds
        rss += s.rss
        stack.push(...(children.get(pid) ?? []))
      }
      const prev = this.last.get(root)
      const cpu = prev ? Math.max(0, ((cpuSeconds - prev.cpuSeconds) / ((now - prev.at) / 1000)) * 100) : 0
      this.last.set(root, { cpuSeconds, at: now })
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

const MEM_UNITS: Record<string, number> = { b: 1, k: 1024, kb: 1024, m: 1024 ** 2, mb: 1024 ** 2, g: 1024 ** 3, gb: 1024 ** 3 }

/** A memory size as docker / compose write it (`1G`, `512m`, `1.5gb`, bytes as a number): binary units. Undefined if invalid. */
export function parseMemSize(value: string | number): number | undefined {
  if (typeof value === "number") return Number.isFinite(value) && value > 0 ? value : undefined
  const m = /^(\d+(?:\.\d+)?)\s*([a-z]*)$/i.exec(value.trim())
  if (!m) return undefined
  const unit = MEM_UNITS[m[2]!.toLowerCase() || "b"]
  const n = Number(m[1]) * (unit ?? NaN)
  return Number.isFinite(n) && n > 0 ? Math.round(n) : undefined
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
      const [used, max] = row.MemUsage.split("/")
      out.set(id, { cpu: Number.parseFloat(row.CPUPerc) || 0, mem: parseSize(used ?? ""), limit: containerLimit(max ?? "") })
    } catch {
      // ignore malformed rows
    }
  }
  return out
}

/** The second half of `MemUsage`; a container without a limit reports the host's memory, which is not one. */
export function containerLimit(text: string, host = totalmem()): number | undefined {
  const n = parseSize(text)
  return n > 0 && n < host * 0.95 ? n : undefined
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
