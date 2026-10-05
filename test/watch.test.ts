import { describe, expect, test } from "bun:test"
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadConfig } from "../src/config/load.ts"
import type { OrbitConfig, ServiceConfig, WatchConfig } from "../src/config/schema.ts"
import { Supervisor } from "../src/core/supervisor.ts"
import { FileWatcher, watchRoots } from "../src/core/watch.ts"
process.env.XDG_STATE_HOME = mkdtempSync(`${tmpdir()}/orbit-state-`)

const cfg = (extra: Partial<WatchConfig> = {}): WatchConfig => ({ paths: ["src/**", "package.json"], ignore: [], debounce: 50, cooldown: 300, ...extra })

function watcher(extra: Partial<WatchConfig> = {}) {
  const triggers: string[][] = []
  const w = new FileWatcher("/nowhere", cfg(extra), { onTrigger: (f) => triggers.push(f), onError: () => {} })
  return { w, triggers }
}

describe("watchRoots", () => {
  test("uses the static prefix of each pattern", () => {
    expect(watchRoots(["src/**"])).toEqual([{ dir: "src", recursive: true }])
    expect(watchRoots(["package.json"])).toEqual([{ dir: "", recursive: false }])
    expect(watchRoots(["src/main.go"])).toEqual([{ dir: "src", recursive: false }])
    expect(watchRoots(["**/*.go", "src/**"])).toEqual([{ dir: "", recursive: true }])
    expect(watchRoots(["src/**", "package.json"])).toEqual([
      { dir: "src", recursive: true },
      { dir: "", recursive: false },
    ])
  })
})

describe("FileWatcher", () => {
  test("matches paths, honours ignore and the default ignores", async () => {
    const { w, triggers } = watcher({ ignore: ["src/**/*.test.js"] })
    w.touch("README.md")
    w.touch("src/a.test.js")
    w.touch("src/.git/x")
    w.touch("src/file.swp")
    await Bun.sleep(120)
    expect(triggers).toEqual([])
    w.touch("src/a.js")
    w.touch("package.json")
    await Bun.sleep(120)
    expect(triggers).toEqual([["src/a.js", "package.json"]])
    w.close()
  })

  test("a burst of changes is one trigger; the debounce restarts with each change", async () => {
    const { w, triggers } = watcher({ debounce: 100 })
    for (let i = 0; i < 5; i++) {
      w.touch(`src/f${i}.js`)
      await Bun.sleep(60) // always inside the debounce window
    }
    expect(triggers).toEqual([])
    await Bun.sleep(160)
    expect(triggers).toHaveLength(1)
    expect(triggers[0]).toHaveLength(5)
    w.close()
  })

  test("cooldown delays and batches the next trigger", async () => {
    const { w, triggers } = watcher({ debounce: 30, cooldown: 300 })
    w.touch("src/a.js")
    await Bun.sleep(80)
    expect(triggers).toHaveLength(1)
    w.touch("src/b.js")
    await Bun.sleep(80)
    w.touch("src/c.js")
    await Bun.sleep(100)
    expect(triggers).toHaveLength(1) // still cooling down
    await Bun.sleep(250)
    expect(triggers).toEqual([["src/a.js"], ["src/b.js", "src/c.js"]])
    w.close()
  })

  test("paused: nothing fires and the queue is dropped", async () => {
    const { w, triggers } = watcher()
    w.touch("src/a.js")
    w.paused = true
    w.touch("src/b.js")
    await Bun.sleep(120)
    expect(triggers).toEqual([])
    expect(w.pending).toBe(false)
    w.paused = false
    w.touch("src/c.js")
    await Bun.sleep(120)
    expect(triggers).toEqual([["src/c.js"]])
    w.close()
  })

  test("sees real file writes", async () => {
    const dir = mkdtempSync(join(tmpdir(), "orbit-watch-"))
    mkdirSync(join(dir, "src/deep"), { recursive: true })
    const triggers: string[][] = []
    const w = new FileWatcher(dir, cfg(), { onTrigger: (f) => triggers.push(f), onError: () => {} })
    w.start()
    await Bun.sleep(100)
    writeFileSync(join(dir, "src/deep/x.go"), "1")
    writeFileSync(join(dir, "other.txt"), "1")
    await Bun.sleep(300)
    expect(triggers).toEqual([["src/deep/x.go"]])
    w.close()
  })
})

describe("watch config", () => {
  function load(yaml: string) {
    const dir = mkdtempSync(join(tmpdir(), "orbit-cfg-"))
    writeFileSync(join(dir, "orbit.yaml"), yaml)
    return loadConfig({ dir, env: {} })
  }

  test("short and long forms", () => {
    const c = load(`services:
  a: { cmd: x, watch: ["src/**", "go.mod"] }
  b: { cmd: x, watch: "*.js" }
  c:
    cmd: x
    watch: { paths: [src/**], ignore: ["**/*.test.js"], debounce: 2s, cooldown: 1m }
  d: { cmd: x }
`)
    expect(c.services.a!.watch).toEqual({ paths: ["src/**", "go.mod"], ignore: [], debounce: 1000, cooldown: 10_000 })
    expect(c.services.b!.watch!.paths).toEqual(["*.js"])
    expect(c.services.c!.watch).toEqual({ paths: ["src/**"], ignore: ["**/*.test.js"], debounce: 2000, cooldown: 60_000 })
    expect(c.services.d!.watch).toBeUndefined()
  })

  test("empty paths is an error", () => {
    expect(() => load(`services:\n  a: { cmd: x, watch: { ignore: [a] } }\n`)).toThrow("at least one path")
  })
})

describe("supervisor watch", () => {
  function setup(w: Partial<WatchConfig> = {}) {
    const dir = mkdtempSync(join(tmpdir(), "orbit-sup-"))
    const svc: ServiceConfig = {
      name: "api", type: "process", cmd: "sleep 30", cwd: dir, env: {}, envFiles: [], dependsOn: [], restart: "no",
      startTimeout: 5000, stopTimeout: 1000, autostart: true, ports: [], volumes: [], dockerArgs: [],
      watch: { paths: ["*.go"], ignore: [], debounce: 40, cooldown: 100, ...w },
    }
    const config: OrbitConfig = { name: "t", root: dir, services: { api: svc }, groups: {} }
    return { dir, sup: new Supervisor(config) }
  }

  test("a change restarts a running service, but never starts a stopped one", async () => {
    const { dir, sup } = setup()
    await sup.init()
    writeFileSync(join(dir, "a.go"), "1")
    await Bun.sleep(400)
    expect(sup.state("api").status).toBe("stopped")

    await sup.start("api")
    const pid = sup.state("api").pid
    await Bun.sleep(150)
    writeFileSync(join(dir, "b.go"), "1")
    await Bun.sleep(600)
    expect(sup.state("api").status).toBe("running")
    expect(sup.state("api").pid).not.toBe(pid)
    expect(sup.state("api").restarts).toBe(0)
    expect(sup.logs.lines("api").some((l) => l.text.startsWith("↻ b.go changed"))).toBe(true)
    await sup.dispose()
  })

  test("toggleWatch pauses restarts", async () => {
    const { dir, sup } = setup()
    await sup.init()
    await sup.start("api")
    const pid = sup.state("api").pid
    expect(sup.toggleWatch("api")).toBe("paused")
    expect(sup.state("api").watch).toBe("paused")
    writeFileSync(join(dir, "c.go"), "1")
    await Bun.sleep(400)
    expect(sup.state("api").pid).toBe(pid)
    expect(sup.toggleWatch("api")).toBe("active")
    await sup.dispose()
  })
})
