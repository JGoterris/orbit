import { describe, expect, test } from "bun:test"
import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadConfig } from "../src/config/load.ts"
import { containerLimit, parseMemSize } from "../src/core/metrics.ts"
import { BUCKET_MS, BUCKETS, detectLeak, memLevel, resample, ResourceHistory, type ResourceBucket } from "../src/core/resources.ts"

const MB = 1024 ** 2
const MIN = 60_000

/** One bucket per 10 s for `minutes`, with memory given by `mem(minute)`. */
function series(minutes: number, mem: (minute: number) => number, saw = 0): ResourceBucket[] {
  const out: ResourceBucket[] = []
  for (let t = 0; t <= minutes * MIN; t += BUCKET_MS) {
    const base = mem(t / MIN)
    const wobble = saw ? ((t / BUCKET_MS) % 6) * saw : 0 // garbage collector sawtooth: peaks above the floor
    out.push({ t, cpu: 1, cpuMax: 1, mem: base + wobble / 2, memMin: base, memMax: base + wobble })
  }
  return out
}

describe("resource history", () => {
  test("samples are averaged into 10 s buckets and the ring keeps an hour", () => {
    const h = new ResourceHistory()
    h.push("a", 10, 100, 0)
    h.push("a", 30, 300, 2000)
    h.push("a", 0, 200, 4000)
    const [b] = h.buckets("a")
    expect(b).toMatchObject({ t: 0, cpuMax: 30, memMin: 100, memMax: 300 })
    expect(b!.cpu).toBeCloseTo(40 / 3)
    expect(b!.mem).toBeCloseTo(200)
    for (let i = 1; i <= BUCKETS + 5; i++) h.push("a", 1, 1, i * BUCKET_MS)
    expect(h.buckets("a").length).toBe(BUCKETS)
    expect(h.buckets("a", 60_000, (BUCKETS + 5) * BUCKET_MS).length).toBe(7)
    h.clear("a")
    expect(h.buckets("a")).toEqual([])
  })

  test("resample averages down and keeps short series", () => {
    expect(resample([1, 2, 3], 5)).toEqual([1, 2, 3])
    expect(resample([1, 3, 5, 7], 2)).toEqual([2, 6])
    expect(resample([1, 2], 0)).toEqual([])
  })
})

describe("memory level", () => {
  test("warn at 85 %, over at 100 %, with hysteresis", () => {
    expect(memLevel(500, undefined)).toBeUndefined()
    expect(memLevel(800, 1000)).toBeUndefined()
    expect(memLevel(850, 1000)).toBe("warn")
    expect(memLevel(1000, 1000)).toBe("over")
    expect(memLevel(820, 1000, "warn")).toBe("warn")
    expect(memLevel(820, 1000)).toBeUndefined()
    expect(memLevel(790, 1000, "warn")).toBeUndefined()
  })
})

describe("leak detection", () => {
  test("steady growth is a leak, with the time left before the limit", () => {
    const leak = detectLeak(series(20, (m) => 100 * MB + m * 5 * MB), 400 * MB)
    expect(leak).toBeDefined()
    expect(leak!.perMin / MB).toBeCloseTo(5, 0)
    expect(leak!.etaMs! / MIN).toBeCloseTo(40, 0) // 200 MB to go at 5 MB/min
  })

  test("a garbage collector sawtooth around a flat floor is not", () => {
    expect(detectLeak(series(30, () => 200 * MB, 30 * MB))).toBeUndefined()
  })

  test("a leak still shows through the sawtooth", () => {
    expect(detectLeak(series(25, (m) => 100 * MB + m * 4 * MB, 30 * MB))).toBeDefined()
  })

  test("needs 15 minutes of data and a real amount of growth", () => {
    expect(detectLeak(series(10, (m) => 100 * MB + m * 20 * MB))).toBeUndefined()
    expect(detectLeak(series(25, (m) => 100 * MB + m * 0.1 * MB))).toBeUndefined()
  })

  test("growth that stopped long ago is not a leak", () => {
    expect(detectLeak(series(40, (m) => 100 * MB + Math.min(m, 8) * 20 * MB))).toBeUndefined()
  })

  test("a step up and then noise is not a leak", () => {
    expect(detectLeak(series(30, (m) => (m < 15 ? 100 * MB : 300 * MB + ((m * 7) % 5) * MB)))).toBeUndefined()
  })
})

describe("memory sizes", () => {
  test("docker style sizes are binary", () => {
    expect(parseMemSize("1G")).toBe(1024 ** 3)
    expect(parseMemSize("512m")).toBe(512 * MB)
    expect(parseMemSize("1.5gb")).toBe(1.5 * 1024 ** 3)
    expect(parseMemSize(1048576)).toBe(1048576)
    expect(parseMemSize("lots")).toBeUndefined()
    expect(parseMemSize("0")).toBeUndefined()
  })

  test("a container reporting the host's memory has no limit", () => {
    expect(containerLimit("1GiB", 16 * 1024 ** 3)).toBe(1024 ** 3)
    expect(containerLimit("15.6GiB", 16 * 1024 ** 3)).toBeUndefined()
  })
})

describe("mem_limit in config", () => {
  const project = (files: Record<string, string>) => {
    const dir = mkdtempSync(join(tmpdir(), "orbit-mem-"))
    for (const [n, c] of Object.entries(files)) writeFileSync(join(dir, n), c)
    return dir
  }

  test("orbit.yaml: mem_limit and leak_detection", () => {
    const cfg = loadConfig({ dir: project({ "orbit.yaml": "services:\n  a: { cmd: sleep 1, mem_limit: 1G }\n  b: { cmd: sleep 1, leak_detection: false }\n" }) })
    expect(cfg.services.a!.memLimit).toBe(1024 ** 3)
    expect(cfg.services.a!.leakDetection).toBe(true)
    expect(cfg.services.b!.memLimit).toBeUndefined()
    expect(cfg.services.b!.leakDetection).toBe(false)
  })

  test("compose: mem_limit and deploy.resources.limits.memory, overridable from orbit.yaml", () => {
    const dir = project({
      "docker-compose.yml": "services:\n  db: { image: pg, mem_limit: 512m }\n  web: { image: nginx, deploy: { resources: { limits: { memory: 2G } } } }\n  q: { image: redis }\n",
      "orbit.yaml": "services:\n  q: { mem_limit: 64m }\n",
    })
    const cfg = loadConfig({ dir })
    expect(cfg.services.db!.memLimit).toBe(512 * MB)
    expect(cfg.services.web!.memLimit).toBe(2 * 1024 ** 3)
    expect(cfg.services.q!.memLimit).toBe(64 * MB)
  })

  test("an invalid size is a config error", () => {
    expect(() => loadConfig({ dir: project({ "orbit.yaml": "services:\n  a: { cmd: sleep 1, mem_limit: lots }\n" }) })).toThrow("invalid memory size")
  })
})
