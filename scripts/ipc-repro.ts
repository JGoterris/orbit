// Temporary: runs sequences of real IpcClient requests against a real IpcServer and reports which ones get answered.
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import type { OrbitConfig, ServiceConfig } from "../src/config/schema.ts"
import { IpcClient } from "../src/core/ipc/client.ts"
import { IpcServer } from "../src/core/ipc/server.ts"
import { Supervisor } from "../src/core/supervisor.ts"

const svc = (name: string): ServiceConfig =>
  ({ name, type: "process", cmd: "bun -e 0", cwd: process.cwd(), env: {}, envFiles: [], dependsOn: [], restart: "no", startTimeout: 5000, stopTimeout: 1000, autostart: true, leakDetection: true, ports: [], volumes: [], dockerArgs: [] }) as ServiceConfig
const config = (): OrbitConfig => ({ name: "repro", root: process.cwd(), services: { a: svc("a") }, groups: { all: ["a"] } })

type Step = [method: string, params: Record<string, unknown>, pause?: number]
const OK_A: Step = ["history", { service: "a" }]
const SINCE: Step = ["history", { service: "a", since: 1 }]
const NOPE: Step = ["history", { service: "nope" }]
const NOPE_START: Step = ["start", { services: ["nope"] }]

const variants: Record<string, Step[]> = {
  "1 exact failing sequence": [OK_A, SINCE, NOPE],
  "2 nope first": [NOPE],
  "3 ok then nope": [OK_A, NOPE],
  "4 since then nope": [SINCE, NOPE],
  "5 ok, ok, nope (no since)": [OK_A, OK_A, NOPE],
  "6 exact sequence, 50ms pauses": [OK_A, ["history", { service: "a", since: 1 }, 50], ["history", { service: "nope" }, 50]],
  "7 ok then start-nope": [OK_A, NOPE_START],
  "8 exact sequence again (determinism)": [OK_A, SINCE, NOPE],
}

async function timeout<T>(p: Promise<T>, ms: number): Promise<T | "HANG"> {
  return Promise.race([p, new Promise<"HANG">((r) => setTimeout(() => r("HANG"), ms))])
}

for (const [name, steps] of Object.entries(variants)) {
  const sup = new Supervisor(config(), { stateDir: mkdtempSync(join(tmpdir(), "orbit-ipcrepro-")) })
  const server = new IpcServer(sup)
  await server.start()
  const client = await IpcClient.connect(server.path)
  const out: string[] = []
  for (const [method, params, pause] of steps) {
    if (pause) await Bun.sleep(pause)
    const r = await timeout(client.request(method as never, params).then(() => "ok", (e: Error) => `err(${e.message.slice(0, 30)})`), 1500)
    out.push(r)
    if (r === "HANG") break
  }
  console.log(out.includes("HANG") ? "HANG" : "OK  ", name, JSON.stringify(out))
  client.close()
  server.close()
  await sup.dispose()
}
process.exit(0)
