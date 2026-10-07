import { describe, expect, test } from "bun:test"
import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { describeDiff, diffConfig, isEmptyDiff } from "../src/config/diff.ts"
import type { OrbitConfig, ServiceConfig } from "../src/config/schema.ts"
import { RemoteSupervisor } from "../src/core/ipc/remote.ts"
import { IpcServer } from "../src/core/ipc/server.ts"
import { Supervisor } from "../src/core/supervisor.ts"
import { sleepCmd, win } from "./helpers.ts"

process.env.XDG_STATE_HOME = mkdtempSync(`${tmpdir()}/orbit-state-`) // tests must not touch the real ~/.local/state

function svc(name: string, cmd: string, extra: Partial<ServiceConfig> = {}): ServiceConfig {
  return {
    name, type: "process", cmd, cwd: process.cwd(), env: {}, envFiles: [], dependsOn: [], restart: "no",
    startTimeout: 5000, stopTimeout: 1000, autostart: true, leakDetection: true, ports: [], volumes: [], dockerArgs: [], ...extra,
  }
}

function config(...services: ServiceConfig[]): OrbitConfig {
  return { name: "t", root: process.cwd(), services: Object.fromEntries(services.map((s) => [s.name, s])), groups: {} }
}

describe("diffConfig", () => {
  test("equal configs have an empty diff, whatever the key order", () => {
    const a = config(svc("a", "x", { env: { A: "1", B: "2" } }))
    const b = config(svc("a", "x", { env: { B: "2", A: "1" } }))
    expect(isEmptyDiff(diffConfig(a, b))).toBe(true)
  })

  test("added, removed and changed; only a definition change needs a restart", () => {
    const prev = config(svc("a", "x"), svc("b", "y"), svc("c", "z"), svc("d", "w"))
    const next = config(svc("a", "x2"), svc("c", "z", { description: "hi" }), svc("d", "w"), svc("e", "v"))
    const d = diffConfig(prev, next)
    expect(d.added).toEqual(["e"])
    expect(d.removed).toEqual(["b"])
    expect(d.changed).toEqual([
      { name: "a", fields: ["cmd"], restart: true },
      { name: "c", fields: ["description"], restart: false },
    ])
    expect(describeDiff(d)).toEqual(["+ e", "- b", "~ a (cmd) ↻", "~ c (description)"])
  })

  test("a compose block that changed shows up through its hash", () => {
    const d = diffConfig(config(svc("a", "x", { composeHash: "1" })), config(svc("a", "x", { composeHash: "2" })))
    expect(d.changed).toEqual([{ name: "a", fields: ["composeHash"], restart: true }])
  })

  test("a renamed project is reported, not applied", () => {
    const d = diffConfig(config(svc("a", "x")), { ...config(svc("a", "x")), name: "other" })
    expect(d.nameChanged).toEqual({ from: "t", to: "other" })
  })
})

describe.skipIf(win)("reload", () => {
  test("restarts only what changed, stops the removed, starts the added", async () => {
    const sup = new Supervisor(config(
      svc("db", sleepCmd(30)),
      svc("api", sleepCmd(30), { dependsOn: ["db"] }),
      svc("old", sleepCmd(30)),
    ), { stateDir: mkdtempSync(join(tmpdir(), "orbit-reload-")) })
    await sup.init()
    await sup.startAll()
    const pids = { db: sup.state("db").pid, api: sup.state("api").pid }

    const diff = await sup.reload(config(
      svc("db", sleepCmd(31)), // restart
      svc("api", sleepCmd(30), { dependsOn: ["db"], description: "hot" }), // hot: keeps running
      svc("fresh", sleepCmd(30)),
      svc("lazy", sleepCmd(30), { autostart: false }),
    ))
    expect(diff.removed).toEqual(["old"])
    expect(diff.added).toEqual(["fresh", "lazy"])
    for (let i = 0; i < 40 && !(sup.state("db").status === "running" && sup.state("fresh").status === "running"); i++) await Bun.sleep(100)

    expect(sup.names.sort()).toEqual(["api", "db", "fresh", "lazy"])
    expect(sup.state("db").pid).not.toBe(pids.db)
    expect(sup.state("api").pid).toBe(pids.api)
    expect(sup.state("api").status).toBe("running")
    expect(sup.service("api").description).toBe("hot")
    expect(sup.state("fresh").status).toBe("running")
    expect(sup.state("lazy").status).toBe("stopped")
    expect(sup.order.indexOf("db")).toBeLessThan(sup.order.indexOf("api"))
    await sup.dispose()
  })

  test("a change on disk is offered as pending, and reload() applies it", async () => {
    const root = mkdtempSync(join(tmpdir(), "orbit-yaml-"))
    const file = join(root, "orbit.yaml")
    writeFileSync(file, `services:\n  a:\n    cmd: ${sleepCmd(30)}\n`)
    const { loadConfig } = await import("../src/config/load.ts")
    const sup = new Supervisor(loadConfig({ file }), { stateDir: mkdtempSync(join(tmpdir(), "orbit-reload-")) })
    await sup.init()
    const seen: unknown[] = []
    sup.on("configPending", (p) => seen.push(p))

    writeFileSync(file, `services:\n  a:\n    cmd: ${sleepCmd(31)}\n`)
    for (let i = 0; i < 30 && !sup.pendingConfig(); i++) await Bun.sleep(100)
    expect(sup.pendingConfig()?.diff?.changed).toEqual([{ name: "a", fields: ["cmd"], restart: true }])

    writeFileSync(file, "services: [oops")
    for (let i = 0; i < 30 && !sup.pendingConfig()?.error; i++) await Bun.sleep(100)
    expect(sup.pendingConfig()?.error).toContain("invalid YAML")

    writeFileSync(file, `services:\n  a:\n    cmd: ${sleepCmd(31)}\n`)
    for (let i = 0; i < 30 && !sup.pendingConfig()?.diff; i++) await Bun.sleep(100)
    const diff = await sup.reload()
    expect(diff.changed.map((c) => c.name)).toEqual(["a"])
    expect(sup.pendingConfig()).toBeUndefined()
    expect(sup.service("a").cmd).toBe(sleepCmd(31))
    await sup.dispose()
  })

  test("a remote supervisor follows the reload", async () => {
    const sup = new Supervisor(config(svc("a", sleepCmd(30))), { stateDir: mkdtempSync(join(tmpdir(), "orbit-reload-")) })
    const server = new IpcServer(sup)
    expect(await server.start()).toBe(true)
    const remote = await RemoteSupervisor.connect(server.path)
    await remote.init()
    const diff = await remote.reload(config(svc("a", sleepCmd(30)), svc("b", sleepCmd(30), { autostart: false })))
    expect(diff.added).toEqual(["b"])
    for (let i = 0; i < 20 && !remote.names.includes("b"); i++) await Bun.sleep(50)
    expect(remote.names).toEqual(["a", "b"])
    expect(remote.state("b").status).toBe("stopped")
    remote.detach()
    server.close()
    await sup.dispose()
  })
})
