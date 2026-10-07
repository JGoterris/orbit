// Temporary (Windows diagnosis): copies of "ipc > history: buckets" with one variation each. Only runs with ORBIT_REPRO=1.
import { afterEach, describe, expect, test } from "bun:test"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import type { OrbitConfig, ServiceConfig } from "../src/config/schema.ts"
import { IpcClient } from "../src/core/ipc/client.ts"
import { IpcServer } from "../src/core/ipc/server.ts"
import { Supervisor } from "../src/core/supervisor.ts"
import { sleepCmd } from "./helpers.ts"

const svc = (name: string, cmd: string): ServiceConfig =>
  ({ name, type: "process", cmd, cwd: process.cwd(), env: {}, envFiles: [], dependsOn: [], restart: "no", startTimeout: 5000, stopTimeout: 1000, autostart: true, leakDetection: true, ports: [], volumes: [], dockerArgs: [] }) as ServiceConfig
const config = (): OrbitConfig => ({ name: "repro", root: process.cwd(), services: { a: svc("a", sleepCmd(30)) }, groups: { all: ["a"] } })

const cleanup: Array<() => unknown> = []
afterEach(async () => {
  for (const fn of cleanup.splice(0).reverse()) await fn()
})

async function setup() {
  const sup = new Supervisor(config(), { stateDir: mkdtempSync(join(tmpdir(), "orbit-ipc-")) })
  const server = new IpcServer(sup)
  expect(await server.start()).toBe(true)
  const client = await IpcClient.connect(server.path)
  cleanup.push(async () => {
    client.close()
    server.close()
    await sup.dispose()
  })
  return { sup, client }
}
const push = (sup: Supervisor) => (sup as unknown as { resHistory: { push(n: string, c: number, m: number): void } }).resHistory.push("a", 5, 1000)
const settle = (client: IpcClient, params: Record<string, unknown>) =>
  Promise.race([client.request("history", params).then(() => "ok", (e: Error) => e.message), Bun.sleep(1500).then(() => "HANG")])

describe.skipIf(!process.env.ORBIT_REPRO)("ipc repro", () => {
  test("T1 exact copy", async () => {
    const { client, sup } = await setup()
    push(sup)
    const buckets = await client.request<Array<{ cpu: number; mem: number }>>("history", { service: "a" })
    expect(buckets.length).toBe(1)
    expect(await client.request<unknown[]>("history", { service: "a", since: 1 })).toEqual([])
    await expect(client.request("history", { service: "nope" })).rejects.toThrow('unknown service "nope"')
  })

  test("T2 no resHistory.push", async () => {
    const { client } = await setup()
    await client.request("history", { service: "a" })
    expect(await client.request<unknown[]>("history", { service: "a", since: 1 })).toEqual([])
    await expect(client.request("history", { service: "nope" })).rejects.toThrow('unknown service "nope"')
  })

  test("T3 same as T1 but the last request is not wrapped in expect().rejects", async () => {
    const { client, sup } = await setup()
    push(sup)
    await client.request("history", { service: "a" })
    await client.request("history", { service: "a", since: 1 })
    expect(await settle(client, { service: "nope" })).toBe('unknown service "nope"')
  })

  test("T4 only pushed data, then the error", async () => {
    const { client, sup } = await setup()
    push(sup)
    await client.request("history", { service: "a" })
    expect(await settle(client, { service: "nope" })).toBe('unknown service "nope"')
  })

  test("T5 the error first, under bun test", async () => {
    const { client } = await setup()
    expect(await settle(client, { service: "nope" })).toBe('unknown service "nope"')
  })

  test("T6 two errors in a row", async () => {
    const { client } = await setup()
    expect(await settle(client, { service: "nope" })).toBe('unknown service "nope"')
    expect(await settle(client, { service: "nope2" })).toBe('unknown service "nope2"')
  })
})
