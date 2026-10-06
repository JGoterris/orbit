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

  describe("lifecycle hooks", () => {
    const hook = (cmd: string, timeout = 5000) => ({ cmd, timeout })
    const marker = () => `${__mk(`${__tmp()}/orbit-hook-`)}/m`

    test("pre_start runs before cmd and its output reaches the log", async () => {
      const m = marker()
      const sup = new Supervisor(config(
        svc("a", `cat ${m}; sleep 30`, { hooks: { preStart: [hook(`echo prepared | tee ${m}`)], postStart: [], postStop: [] } }),
      ))
      expect(await sup.start("a")).toBe(true)
      await Bun.sleep(150)
      const lines = sup.logs.lines("a").map((l) => l.text)
      expect(lines.filter((t) => t === "prepared")).toHaveLength(2) // from the hook, then from cmd
      expect(lines.some((t) => t.startsWith("✓ pre_start"))).toBe(true)
      await sup.dispose()
    })

    test("a failing pre_start fails the service and its dependents without running cmd", async () => {
      const m = marker()
      const sup = new Supervisor(config(
        svc("a", `touch ${m}; sleep 30`, { hooks: { preStart: [hook("exit 1")], postStart: [], postStop: [] } }),
        svc("b", "sleep 30", { dependsOn: ["a"] }),
      ))
      expect(await sup.start("b")).toBe(false)
      expect(sup.state("a").status).toBe("failed")
      expect(sup.state("a").error).toContain("pre_start failed")
      expect(sup.state("b").status).toBe("failed")
      expect(await Bun.file(m).exists()).toBe(false)
      await sup.dispose()
    })

    test("a failing post_start is reported but the service stays up", async () => {
      const sup = new Supervisor(config(
        svc("a", "sleep 30", { hooks: { preStart: [], postStart: [hook("exit 4")], postStop: [] } }),
      ))
      expect(await sup.start("a")).toBe(true)
      await Bun.sleep(300)
      expect(sup.state("a").status).toBe("running")
      expect(sup.state("a").error).toContain("post_start failed")
      await sup.dispose()
    })

    test("post_stop runs on stop and after a crash, with ORBIT_EXIT_CODE", async () => {
      const m = marker()
      const sup = new Supervisor(config(
        svc("stopped", "sleep 30", { hooks: { preStart: [], postStart: [], postStop: [hook(`echo $ORBIT_SERVICE > ${m}`)] } }),
        svc("crashy", "sleep 0.2; exit 3", { hooks: { preStart: [], postStart: [], postStop: [hook(`echo code=$ORBIT_EXIT_CODE`)] } }),
      ))
      await sup.start("stopped")
      await sup.stop("stopped")
      expect((await Bun.file(m).text()).trim()).toBe("stopped")
      expect(sup.state("stopped").status).toBe("stopped")

      await sup.start("crashy")
      await Bun.sleep(800)
      expect(sup.state("crashy").status).toBe("crashed")
      expect(sup.logs.lines("crashy").some((l) => l.text === "code=3")).toBe(true)
      await sup.dispose()
    })

    test("stopping during a long pre_start kills it", async () => {
      const sup = new Supervisor(config(
        svc("a", "sleep 30", { hooks: { preStart: [hook("sleep 30", 60_000)], postStart: [], postStop: [] } }),
      ))
      const started = sup.start("a")
      await Bun.sleep(300)
      await sup.stop("a")
      expect(await started).toBe(false)
      expect(sup.state("a").status).toBe("stopped")
      await sup.dispose()
    })
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

describe("external services", () => {
  const external = (name: string, port: number, extra: Partial<ServiceConfig> = {}) =>
    svc(name, "", { type: "external", cmd: undefined, health: { http: `http://127.0.0.1:${port}/`, interval: 100, timeout: 300 }, startTimeout: 800, ...extra })
  const freePort = () => 40000 + Math.floor(Math.random() * 20000)

  test("is monitored from init(): healthy while it answers, unhealthy when it stops", async () => {
    const port = freePort()
    let server: ReturnType<typeof Bun.serve> | undefined = Bun.serve({ port, fetch: () => new Response("ok") })
    const sup = new Supervisor(config(external("saas", port)))
    try {
      await sup.init()
      await Bun.sleep(400)
      expect(sup.state("saas").status).toBe("healthy")
      expect(sup.ownedRunningCount()).toBe(0) // quitting orbit has nothing to stop
      await server.stop(true)
      server = undefined
      await Bun.sleep(1000)
      expect(sup.state("saas").status).toBe("unhealthy")
    } finally {
      await server?.stop(true)
      await sup.dispose()
    }
  })

  test("a dependent waits for it; unreachable means the dependent fails", async () => {
    const port = freePort()
    const sup = new Supervisor(config(external("saas", port), svc("api", "sleep 30", { dependsOn: ["saas"] })))
    await sup.init()
    expect(await sup.start("api")).toBe(false)
    expect(sup.state("api").status).toBe("failed")
    expect(sup.state("api").error).toContain("dependency not ready: saas")
    const server = Bun.serve({ port, fetch: () => new Response("ok") })
    try {
      expect(await sup.start("api")).toBe(true)
      expect(sup.state("saas").status).toBe("healthy")
    } finally {
      await server.stop(true)
      await sup.dispose()
    }
  })

  test("stop does nothing to it nor to its dependents; start works without init()", async () => {
    const port = freePort()
    const server = Bun.serve({ port, fetch: () => new Response("ok") })
    const sup = new Supervisor(config(external("saas", port), svc("api", "sleep 30", { dependsOn: ["saas"] })))
    try {
      expect(await sup.start("api")).toBe(true)
      await sup.stop("saas")
      expect(sup.state("saas").status).toBe("healthy")
      expect(sup.state("api").status).toBe("running")
      await sup.stop("api")
      expect(sup.state("api").status).toBe("stopped")
    } finally {
      await server.stop(true)
      await sup.dispose()
    }
  })
})
