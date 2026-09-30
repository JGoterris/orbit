import { describe, expect, test } from "bun:test"
import type { OrbitConfig, ServiceConfig } from "../src/config/schema.ts"
import { Supervisor } from "../src/core/supervisor.ts"

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
})
