import { afterEach, describe, expect, test } from "bun:test"
import { existsSync, mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import type { OrbitConfig, ServiceConfig } from "../src/config/schema.ts"
import { IpcClient } from "../src/core/ipc/client.ts"
import { socketPath } from "../src/core/ipc/endpoint.ts"
import { RemoteSupervisor } from "../src/core/ipc/remote.ts"
import { IpcServer } from "../src/core/ipc/server.ts"
import type { Hello, Notification } from "../src/core/ipc/protocol.ts"
import type { LogLine } from "../src/core/logs.ts"
import { Supervisor, type ServiceState } from "../src/core/supervisor.ts"
import { echoSleepCmd, sleepCmd } from "./helpers.ts"

process.env.XDG_STATE_HOME = mkdtempSync(`${tmpdir()}/orbit-state-`) // tests must not touch the real ~/.local/state

function svc(name: string, cmd: string, extra: Partial<ServiceConfig> = {}): ServiceConfig {
  return {
    name, type: "process", cmd, cwd: process.cwd(), env: {}, envFiles: [], dependsOn: [], restart: "no",
    startTimeout: 5000, stopTimeout: 1000, autostart: true, leakDetection: true, ports: [], volumes: [], dockerArgs: [], ...extra,
  }
}

function config(name: string, ...services: ServiceConfig[]): OrbitConfig {
  return { name, root: process.cwd(), services: Object.fromEntries(services.map((s) => [s.name, s])), groups: { all: services.map((s) => s.name) } }
}

const cleanup: Array<() => unknown> = []
afterEach(async () => {
  for (const fn of cleanup.splice(0).reverse()) await fn()
})

async function setup(name: string, ...services: ServiceConfig[]) {
  const sup = new Supervisor(config(name, ...services), { stateDir: mkdtempSync(join(tmpdir(), "orbit-ipc-")) })
  let shutdown: string | undefined
  const server = new IpcServer(sup, { onShutdown: (how) => (shutdown = how) })
  expect(await server.start()).toBe(true)
  const client = await IpcClient.connect(server.path)
  cleanup.push(async () => {
    client.close()
    server.close()
    await sup.dispose()
  })
  return { sup, server, client, shutdown: () => shutdown }
}

describe("ipc", () => {
  test("hello and snapshot", async () => {
    const { client, sup } = await setup("hello", svc("a", sleepCmd(30)))
    const hello = await client.request<Hello>("hello")
    expect(hello.protocol).toBe(2)
    expect(hello.pid).toBe(process.pid)
    expect(Object.keys(hello.config.services)).toEqual(["a"])
    const snap = await client.request<ServiceState[]>("snapshot")
    expect(snap.map((s) => [s.name, s.status])).toEqual([["a", "stopped"]])
    expect(sup.state("a").status).toBe("stopped")
  })

  test("commands drive the supervisor, groups expand", async () => {
    const { client, sup } = await setup("cmds", svc("a", sleepCmd(30)), svc("b", sleepCmd(30)))
    expect(await client.request<{ ok: boolean }>("start", { services: ["all"] })).toEqual({ ok: true })
    expect(sup.state("a").status).toBe("running")
    expect(sup.state("b").status).toBe("running")
    await client.request("stop", { services: ["a"] })
    expect(sup.state("a").status).toBe("stopped")
    expect(sup.state("b").status).toBe("running")
    await client.request("stopAll")
    expect(sup.state("b").status).toBe("stopped")
  })

  test("history: buckets over the socket, through the remote supervisor", async () => {
    const { client, sup } = await setup("buckets", svc("a", sleepCmd(30)))
    ;(sup as unknown as { resHistory: { push(n: string, c: number, m: number, at?: number): void } }).resHistory.push("a", 5, 1000)
    const buckets = await client.request<Array<{ cpu: number; mem: number }>>("history", { service: "a" })
    expect(buckets.length).toBe(1)
    expect(buckets[0]).toMatchObject({ cpu: 5, mem: 1000 })
    expect(await client.request<unknown[]>("history", { service: "a", since: 1 })).toEqual([])
    await expect(client.request("history", { service: "nope" })).rejects.toThrow('unknown service "nope"')
  })

  test("errors for unknown services and methods", async () => {
    const { client } = await setup("errs", svc("a", sleepCmd(30)))
    await expect(client.request("start", { services: ["nope"] })).rejects.toThrow('unknown service or group "nope"')
    await expect(client.request("start", {})).rejects.toThrow("list of names")
    await expect(client.request("bogus" as never)).rejects.toThrow("unknown method")
  })

  test("subscribers receive state changes and log lines", async () => {
    const { client } = await setup("subs", svc("a", echoSleepCmd("hello-ipc", 30)))
    const seen: Notification[] = []
    client.on("notification", (n: Notification) => seen.push(n))
    await client.request("subscribe", { logs: true })
    await client.request("start", { services: ["a"] })
    await Bun.sleep(300)
    const states = seen.filter((n) => n.method === "state").map((n) => (n.params as { state: ServiceState }).state.status)
    expect(states).toContain("running")
    const logs = seen.filter((n) => n.method === "log").map((n) => (n.params as LogLine).text)
    expect(logs).toContain("hello-ipc")
  })

  test("logs request returns history and honours sinceSeq", async () => {
    const { client, sup } = await setup("hist", svc("a", sleepCmd(30)))
    sup.log("a", "one")
    sup.log("a", "two")
    const all = await client.request<LogLine[]>("logs", { service: "a" })
    expect(all.map((l) => l.text)).toEqual(["one", "two"])
    const rest = await client.request<LogLine[]>("logs", { service: "a", sinceSeq: all[0]!.seq })
    expect(rest.map((l) => l.text)).toEqual(["two"])
  })

  test("shutdown is acknowledged before it runs", async () => {
    const { client, shutdown } = await setup("down", svc("a", sleepCmd(30)))
    expect(await client.request<{ ok: boolean }>("shutdown", { how: "detach" })).toEqual({ ok: true })
    await Bun.sleep(50)
    expect(shutdown()).toBe("detach")
  })

  test("a second server refuses to take a live socket; a stale one is replaced", async () => {
    const { sup, server } = await setup("dup", svc("a", sleepCmd(30)))
    expect(await new IpcServer(sup).start()).toBe(false)
    server.close()
    expect(existsSync(server.path)).toBe(false)
    if (process.platform !== "win32") {
      writeFileSync(server.path, "") // not a listening socket
      const again = new IpcServer(sup)
      expect(await again.start()).toBe(true)
      again.close()
    }
  })

  test("malformed input gets a parse error and keeps the connection", async () => {
    const { server, client } = await setup("bad", svc("a", sleepCmd(30)))
    const { connect } = await import("node:net")
    const raw = connect(server.path)
    cleanup.push(() => raw.destroy())
    const reply = new Promise<string>((resolve) => raw.once("data", (d) => resolve(String(d))))
    raw.write("not json\n")
    expect(JSON.parse(await reply).error.code).toBe(-32700)
    expect((await client.request<Hello>("hello")).protocol).toBe(2)
  })
})

describe("socketPath", () => {
  test("lives in the state dir when it fits", () => {
    if (process.platform === "win32") return
    expect(socketPath("/tmp/x")).toBe("/tmp/x/orbit.sock")
  })

  test("falls back to a short path when the state dir is too long", () => {
    if (process.platform === "win32") return
    const long = "/tmp/" + "a".repeat(120)
    const p = socketPath(long)
    expect(Buffer.byteLength(p)).toBeLessThan(104)
    expect(p.endsWith(".sock")).toBe(true)
    expect(socketPath(long)).toBe(p) // deterministic: clients find the same path
  })
})

describe("RemoteSupervisor", () => {
  async function remote(name: string, ...services: ServiceConfig[]) {
    const ctx = await setup(name, ...services)
    const rs = await RemoteSupervisor.connect(ctx.server.path)
    cleanup.push(() => rs.detach())
    await rs.init()
    return { ...ctx, rs }
  }
  const until = async (cond: () => boolean, ms = 3000) => {
    const end = Date.now() + ms
    while (!cond() && Date.now() < end) await Bun.sleep(20)
    expect(cond()).toBe(true)
  }

  test("mirrors config, graph and state; commands reach the real supervisor", async () => {
    const { rs, sup } = await remote("mirror", svc("db", sleepCmd(30)), svc("api", sleepCmd(30), { dependsOn: ["db"] }))
    expect(rs.names).toEqual(["db", "api"])
    expect(rs.order).toEqual(["db", "api"])
    expect(rs.dependents.db).toEqual(["api"])
    expect(rs.stateDir).toBe(sup.stateDir)
    expect(rs.state("api").status).toBe("stopped")

    const changes: Array<string | undefined> = []
    rs.on("change", (n?: string) => changes.push(n))
    expect(await rs.start("api")).toBe(true) // starts db first, like a local one
    await until(() => rs.state("api").status === "running" && rs.state("db").status === "running")
    expect(sup.state("api").status).toBe("running")
    expect(rs.isUp("api")).toBe(true)
    expect(rs.ownedRunningCount()).toBe(2)
    expect(changes).toContain("api")

    await rs.stop("db") // stops dependents first
    await until(() => !rs.isUp("api") && !rs.isUp("db"))
  })

  test("a late client sees what already happened: state and log history", async () => {
    const { server, sup } = await setup("late", svc("a", echoSleepCmd("before", 30)))
    await sup.start("a")
    await until(() => sup.logs.lines("a").some((l) => l.text === "before"))
    const rs = await RemoteSupervisor.connect(server.path)
    cleanup.push(() => rs.detach())
    await rs.init()
    expect(rs.state("a").status).toBe("running")
    expect(rs.state("a").pid).toBe(sup.state("a").pid!)
    expect(rs.logs.lines("a").map((l) => l.text)).toContain("before")

    // and live lines keep coming, once each
    sup.log("a", "after")
    await until(() => rs.logs.lines("a").some((l) => l.text === "after"))
    expect(rs.logs.lines("a").filter((l) => l.text === "after").length).toBe(1)
  })

  test("clearing logs on either side clears the mirror", async () => {
    const { rs, sup } = await remote("clear", svc("a", sleepCmd(30)))
    sup.log("a", "x")
    await until(() => rs.logs.lines("a").length === 1)
    sup.clearLogs("a") // the server only broadcasts `cleared` for requests, so use the mirror's own path
    rs.clearLogs("a")
    expect(rs.logs.lines("a")).toEqual([])
    await until(() => sup.logs.lines("a").length === 0)
  })

  test("dispose asks the daemon to quit and waits for the hang-up; detach leaves it alone", async () => {
    const a = await remote("quit", svc("a", sleepCmd(30)))
    const closed = new Promise<void>((r) => a.rs.once("close", () => r()))
    a.rs.detach()
    await closed
    expect(a.rs.connected).toBe(false)
    expect(a.shutdown()).toBeUndefined() // detaching is not shutting the daemon down

    const b = await remote("quit2", svc("a", sleepCmd(30)))
    // the test server only records the request: closing the socket is what a real daemon does next
    const disposing = b.rs.dispose()
    await until(() => b.shutdown() === "stop")
    b.server.close()
    await disposing
    expect(b.rs.connected).toBe(false)
  })

  test("refuses a daemon that speaks another protocol", async () => {
    const { server } = await setup("proto", svc("a", sleepCmd(30)))
    const real = IpcClient.prototype.request
    IpcClient.prototype.request = async function (this: IpcClient, method, params) {
      const res = await real.call(this, method, params)
      return method === "hello" ? { ...(res as object), protocol: 99, version: "9.9.9" } : res
    } as typeof real
    try {
      await expect(RemoteSupervisor.connect(server.path)).rejects.toThrow("protocol 99")
    } finally {
      IpcClient.prototype.request = real
    }
  })
})
