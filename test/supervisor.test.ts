import { describe, expect, test } from "bun:test"
import type { OrbitConfig, ServiceConfig } from "../src/config/schema.ts"
import { readState, writeState } from "../src/core/state.ts"
import { Supervisor } from "../src/core/supervisor.ts"
import { mkdtempSync as __mk } from "node:fs"
import { tmpdir as __tmp } from "node:os"
process.env.XDG_STATE_HOME = __mk(`${__tmp()}/orbit-state-`) // tests must not touch the real ~/.local/state

function svc(name: string, cmd: string, extra: Partial<ServiceConfig> = {}): ServiceConfig {
  return {
    name, type: "process", cmd, cwd: process.cwd(), env: {}, envFiles: [], dependsOn: [], restart: "no",
    startTimeout: 5000, stopTimeout: 1000, autostart: true, ports: [], volumes: [], dockerArgs: [], ...extra,
  }
}

function config(...services: ServiceConfig[]): OrbitConfig {
  return { name: "t", root: process.cwd(), services: Object.fromEntries(services.map((s) => [s.name, s])), groups: {} }
}

describe("supervisor", () => {
  test("starts dependencies first and stops dependents first", async () => {
    const started: string[] = []
    const sup = new Supervisor(config(
      svc("db", "echo db-up; sleep 30"),
      svc("api", "echo api-up; sleep 30", { dependsOn: ["db"] }),
    ))
    sup.on("change", (n: string) => {
      if (sup.state(n).status === "running" && !started.includes(n)) started.push(n)
    })
    expect(await sup.start("api")).toBe(true)
    expect(started).toEqual(["db", "api"])
    await Bun.sleep(100)
    expect(sup.logs.lines("api").some((l) => l.text === "api-up")).toBe(true)
    await sup.stop("db")
    expect(sup.state("api").status).toBe("stopped")
    expect(sup.state("db").status).toBe("stopped")
    await sup.dispose()
  })

  test("health checks gate readiness", async () => {
    const port = 40000 + Math.floor(Math.random() * 20000)
    const sup = new Supervisor(config(
      svc("srv", `sleep 0.3; exec bun -e 'Bun.serve({port:${port},fetch:()=>new Response("ok")}); setInterval(()=>{},1000)'`, {
        port, health: { http: `http://127.0.0.1:${port}/`, interval: 200, timeout: 500 },
      }),
    ))
    expect(await sup.start("srv")).toBe(true)
    expect(sup.state("srv").status).toBe("healthy")
    await sup.dispose()
  })

  test("crash + restart on-failure, dependents fail when a dependency cannot start", async () => {
    const sup = new Supervisor(config(
      svc("bad", "echo boom >&2; exit 3", { restart: "on-failure", health: { cmd: "false", interval: 100, timeout: 100 } }),
      svc("app", "sleep 30", { dependsOn: ["bad"] }),
    ))
    expect(await sup.start("app")).toBe(false)
    expect(sup.state("app").status).toBe("failed")
    expect(sup.state("bad").status).toBe("crashed")
    expect(sup.logs.lines("bad").some((l) => l.stream === "stderr" && l.text === "boom")).toBe(true)
    await Bun.sleep(1300)
    expect(sup.state("bad").restarts).toBeGreaterThanOrEqual(1)
    await sup.dispose()
    expect(sup.state("bad").status).toBe("stopped")
  })

  test("kills the whole process tree", async () => {
    const sup = new Supervisor(config(svc("tree", "sleep 60 & sleep 60 & wait")))
    await sup.start("tree")
    const pid = sup.state("tree").pid!
    await Bun.sleep(200)
    await sup.stop("tree")
    let alive = true
    try { process.kill(-pid, 0) } catch { alive = false }
    expect(alive).toBe(false)
    await sup.dispose()
  })

  test("env files are re-read on every start and inline env wins", async () => {
    const { mkdtempSync, writeFileSync } = await import("node:fs")
    const { tmpdir } = await import("node:os")
    const dir = mkdtempSync(`${tmpdir()}/orbit-env-`)
    const envPath = `${dir}/.env.local`
    writeFileSync(envPath, "GREETING=hola\nWHO=file")
    const sup = new Supervisor(config(
      svc("echo", 'echo "$GREETING $WHO"; sleep 30', {
        cwd: dir,
        env: { WHO: "inline" },
        envFiles: [{ path: envPath, required: true }],
      }),
    ))
    await sup.start("echo")
    await Bun.sleep(150)
    expect(sup.logs.lines("echo").some((l) => l.text === "hola inline")).toBe(true)
    writeFileSync(envPath, "GREETING=adios")
    await sup.restart("echo")
    await Bun.sleep(150)
    expect(sup.logs.lines("echo").some((l) => l.text === "adios inline")).toBe(true)
    await sup.stop("echo")
    // deleting a required file makes the next start fail with a clear error
    const { rmSync } = await import("node:fs")
    rmSync(envPath)
    expect(await sup.start("echo")).toBe(false)
    expect(sup.state("echo").error).toContain("env_file not found")
    await sup.dispose()
  })

  test("oneshot tasks gate dependents on a successful exit", async () => {
    const sup = new Supervisor(config(
      svc("build", "sleep 0.2; echo built", { oneshot: true, restart: "always" }),
      svc("app", "echo app-up; sleep 30", { dependsOn: ["build"] }),
      svc("broken", "exit 2", { oneshot: true }),
      svc("needs-broken", "sleep 30", { dependsOn: ["broken"] }),
    ))
    expect(await sup.start("app")).toBe(true)
    expect(sup.state("build").status).toBe("exited")
    expect(sup.isReady("build")).toBe(true)
    // already done: starting another dependent does not re-run it
    await sup.stop("app")
    await sup.start("app")
    expect(sup.logs.lines("build").filter((l) => l.text === "built")).toHaveLength(1)
    await Bun.sleep(1200)
    expect(sup.state("build").restarts).toBe(0) // restart: always does not loop a finished task
    expect(await sup.start("needs-broken")).toBe(false)
    expect(sup.state("broken").status).toBe("crashed")
    await sup.dispose()
  })

  test("detach leaves processes running and a new session re-attaches to them", async () => {
    const stateDir = __mk(`${__tmp()}/orbit-detach-`)
    const cfg = () => config(svc("keep", "echo hi; echo oops >&2; sleep 30"))
    const first = new Supervisor(cfg(), { stateDir })
    await first.start("keep")
    const pid = first.state("keep").pid!
    await Bun.sleep(300)
    first.detach()
    first.killAllSync()
    expect(() => process.kill(-pid, 0)).not.toThrow()

    const second = new Supervisor(cfg(), { stateDir })
    await second.init()
    expect(second.state("keep").status).toBe("running")
    expect(second.state("keep").pid).toBe(pid)
    await Bun.sleep(100)
    expect(second.logs.lines("keep").some((l) => l.stream === "stdout" && l.text === "hi")).toBe(true)
    expect(second.logs.lines("keep").some((l) => l.stream === "stderr" && l.text === "oops")).toBe(true)
    await second.stop("keep")
    expect(second.state("keep").status).toBe("stopped")
    expect(() => process.kill(-pid, 0)).toThrow()
    await second.dispose()
    expect(readState(stateDir).services).toEqual({})
  })

  test("an adopted process that exits reports its exit code", async () => {
    const stateDir = __mk(`${__tmp()}/orbit-adopt-exit-`)
    const cfg = () => config(svc("job", "sleep 1.5; exit 3"))
    const first = new Supervisor(cfg(), { stateDir })
    await first.start("job")
    first.detach()
    const second = new Supervisor(cfg(), { stateDir })
    await second.init()
    expect(second.state("job").status).toBe("running")
    await Bun.sleep(3000)
    expect(second.state("job").status).toBe("crashed")
    expect(second.state("job").exitCode).toBe(3)
    await second.dispose()
  })

  test("a recycled pid (different start time) is not adopted", async () => {
    const stateDir = __mk(`${__tmp()}/orbit-stale-`)
    const cfg = () => config(svc("keep", "sleep 30"))
    const first = new Supervisor(cfg(), { stateDir })
    await first.start("keep")
    const pid = first.state("keep").pid!
    first.detach()
    const state = readState(stateDir)
    state.services.keep!.startTime = (state.services.keep!.startTime ?? 0) + 1
    writeState(stateDir, state)
    const second = new Supervisor(cfg(), { stateDir })
    await second.init()
    expect(second.state("keep").status).toBe("stopped")
    process.kill(-pid, "SIGKILL")
    await second.dispose()
  })
})
