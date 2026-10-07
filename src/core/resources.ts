/** 10 s per bucket, 360 buckets = 1 h of history per service. */
export const BUCKET_MS = 10_000
export const BUCKETS = 360

export interface ResourceBucket {
  /** start of the bucket (ms since epoch) */
  t: number
  cpu: number
  cpuMax: number
  mem: number
  memMin: number
  memMax: number
}

export type MemLevel = "warn" | "over"

export interface Leak {
  /** bytes of growth per minute */
  perMin: number
  /** when the sustained growth started (ms since epoch) */
  since: number
  /** ms until the memory limit is reached at this rate, when there is a limit */
  etaMs?: number
}

export interface ResourceInfo {
  memLimit?: number
  level?: MemLevel
  leak?: Leak
}

export const WARN_AT = 0.85
const WARN_OFF = 0.8

/** Memory against its limit; `prev` gives hysteresis so a service hovering at 85 % does not flap. */
export function memLevel(mem: number, limit: number | undefined, prev?: MemLevel): MemLevel | undefined {
  if (!limit || limit <= 0) return undefined
  const ratio = mem / limit
  if (ratio >= 1) return "over"
  if (ratio >= WARN_AT) return "warn"
  if (prev && ratio >= WARN_OFF) return "warn"
  return undefined
}

/** Aggregates 2 s samples into fixed buckets, per service. */
export class ResourceHistory {
  private rings = new Map<string, ResourceBucket[]>()
  private counts = new Map<string, number>()

  push(name: string, cpu: number, mem: number, at = Date.now()) {
    let ring = this.rings.get(name)
    if (!ring) this.rings.set(name, (ring = []))
    const t = Math.floor(at / BUCKET_MS) * BUCKET_MS
    const last = ring[ring.length - 1]
    if (last && last.t === t) {
      const n = (this.counts.get(name) ?? 1) + 1
      this.counts.set(name, n)
      last.cpu += (cpu - last.cpu) / n
      last.mem += (mem - last.mem) / n
      last.cpuMax = Math.max(last.cpuMax, cpu)
      last.memMin = Math.min(last.memMin, mem)
      last.memMax = Math.max(last.memMax, mem)
      return
    }
    this.counts.set(name, 1)
    ring.push({ t, cpu, cpuMax: cpu, mem, memMin: mem, memMax: mem })
    if (ring.length > BUCKETS) ring.shift()
  }

  /** Buckets newer than `sinceMs` ago (all of them by default), oldest first. */
  buckets(name: string, sinceMs?: number, now = Date.now()): ResourceBucket[] {
    const ring = this.rings.get(name) ?? []
    if (sinceMs === undefined) return ring.slice()
    return ring.filter((b) => b.t >= now - sinceMs)
  }

  clear(name: string) {
    this.rings.delete(name)
    this.counts.delete(name)
  }
}

const MIN_MS = 60_000
export const LEAK_MIN_DATA = 15 * MIN_MS
const LEAK_WINDOW = 30 * MIN_MS
const MIN_GROWTH = 20 * 1024 ** 2

/**
 * Sustained memory growth. Looks at the per-minute floor of memory (a garbage collector's sawtooth does not move it)
 * over the last 30 minutes, and asks for a clean upward line: a high R², a real amount of growth and almost no
 * minute-to-minute drops. `buckets` must only hold the current run of the service.
 */
export function detectLeak(buckets: readonly ResourceBucket[], limit?: number): Leak | undefined {
  if (buckets.length < 2) return undefined
  const end = buckets[buckets.length - 1]!.t
  if (end - buckets[0]!.t < LEAK_MIN_DATA) return undefined

  const byMinute = new Map<number, number>()
  for (const b of buckets) {
    if (b.t < end - LEAK_WINDOW) continue
    const minute = Math.floor(b.t / MIN_MS)
    byMinute.set(minute, Math.min(byMinute.get(minute) ?? Infinity, b.memMin))
  }
  const points = [...byMinute].sort((a, b) => a[0] - b[0]).map(([m, v]) => ({ x: m, y: v }))
  if (points.length < 10) return undefined

  const n = points.length
  const mx = points.reduce((s, p) => s + p.x, 0) / n
  const my = points.reduce((s, p) => s + p.y, 0) / n
  let sxx = 0
  let sxy = 0
  let syy = 0
  for (const p of points) {
    sxx += (p.x - mx) ** 2
    sxy += (p.x - mx) * (p.y - my)
    syy += (p.y - my) ** 2
  }
  if (sxx === 0 || syy === 0) return undefined
  const slope = sxy / sxx // bytes per minute
  if (slope <= 0) return undefined
  const r2 = (sxy * sxy) / (sxx * syy)
  if (r2 < 0.8) return undefined

  const first = points[0]!.y
  const last = points[n - 1]!.y
  if (last - first < Math.max(first * 0.1, MIN_GROWTH)) return undefined

  let rising = 0
  for (let i = 1; i < n; i++) if (points[i]!.y >= points[i - 1]!.y) rising++
  if (rising / (n - 1) < 0.7) return undefined

  const leak: Leak = { perMin: slope, since: points[0]!.x * MIN_MS }
  if (limit && last < limit) leak.etaMs = ((limit - last) / slope) * MIN_MS
  return leak
}

export type Range = "2m" | "15m" | "1h"
export const RANGES: readonly Range[] = ["2m", "15m", "1h"]
export const RANGE_MS: Record<Range, number> = { "2m": 120_000, "15m": 900_000, "1h": 3_600_000 }

/** Averages `values` down to at most `n` points (a chart is only as wide as its panel), keeping the newest on the right. */
export function resample(values: readonly number[], n: number): number[] {
  if (n <= 0) return []
  if (values.length <= n) return values.slice()
  const out: number[] = []
  const step = values.length / n
  for (let i = 0; i < n; i++) {
    const from = Math.floor(i * step)
    const to = Math.max(from + 1, Math.floor((i + 1) * step))
    const part = values.slice(from, to)
    out.push(part.reduce((a, b) => a + b, 0) / part.length)
  }
  return out
}
