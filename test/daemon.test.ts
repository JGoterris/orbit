import { afterEach, describe, expect, test } from "bun:test"
import { existsSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadConfig } from "../src/config/load.ts"
import { connectRemote } from "../src/core/ipc/daemon.ts"
import { socketPath } from "../src/core/ipc/endpoint.ts"
import { RemoteSupervisor } from "../src/core/ipc/remote.ts"
import { readLock, stateDir } from "../src/core/state.ts"

process.env.XDG_STATE_HOME = mkdtempSync(`${tmpdir()}/orbit-state-`) // the daemon inherits it: never the real ~/.local/state

function project(name: string, yaml: string) {
  const dir = join(mkdtempSync(`${tmpdir()}/orbit-daemon-`), name)
  mkdirSync(dir)
  writeFileSync(join(dir, "orbit.yaml"), yaml)
  return loadConfig({ dir })
}

const alive = (pid: number) => {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}
const until = async (cond: () => boolean, ms = 8000) => {
  const end = Date.now() + ms
  while (!cond() && Date.now() < end) await Bun.sleep(25)
  expect(cond()).toBe(true)
}

const cleanup: Array<() => unknown> = []
afterEach(async () => {
  for (const fn of cleanup.splice(0).reverse()) await fn()
})

/** Connects (starting the daemon the first time) and makes sure nothing outlives the test. */
async function open(config: ReturnType<typeof project>) {
  const rs: RemoteSupervisor = await connectRemote(config)
  cleanup.push(async () => {
    rs.detach()
    const pid = readLock(stateDir(config))
    if (pid) {
      // last resort: the test failed before it shut the daemon down (connect() never starts one)
      const again = await RemoteSupervisor.connect(socketPath(stateDir(config))).catch(() => undefined)
      await again?.dispose()
      for (let i = 0; i < 100 && alive(pid); i++) await Bun.sleep(25)
      if (alive(pid)) process.kill(pid, "SIGKILL")
    }
  })
  await rs.init()
  return rs
}

describe("orbit daemon", () => {
  test("starts on demand, outlives its clients, supervises with nobody connected, and quits on request", async () => {
    const config = project("d1", "services:\n  w:\n    cmd: sleep 300\n    restart: always\n")
    expect(existsSync(socketPath(stateDir(config)))).toBe(false)

    const first = await open(config)
    const daemonPid = first.hello.pid
    expect(daemonPid).not.toBe(process.pid)
    expect(readLock(stateDir(config))).toBe(daemonPid) // the daemon owns the project
    expect(await first.start("w")).toBe(true)
    await until(() => first.state("w").status === "running")
    const servicePid = first.state("w").pid!

    // "close the terminal": the client goes away, the daemon and the service stay
    first.detach()
    await Bun.sleep(100)
    expect(alive(daemonPid)).toBe(true)
    expect(alive(servicePid)).toBe(true)

    // the service dies while nobody watches: restart: always brings it back
    process.kill(-servicePid, "SIGKILL")
    await until(() => !alive(servicePid))

    // "open another terminal": same daemon, and by now it restarted the service
    const second = await open(config)
    expect(second.hello.pid).toBe(daemonPid)
    await until(() => second.state("w").status === "running" && second.state("w").pid !== servicePid, 10_000)
    expect(second.state("w").restarts).toBe(1)
    const newPid = second.state("w").pid!
    expect(second.logs.lines("w").some((l) => /exited with/.test(l.text))).toBe(true) // history from before it connected

    // quitting for good: services stopped, daemon gone, lock and socket released
    await second.dispose()
    await until(() => !alive(daemonPid))
    expect(alive(newPid)).toBe(false)
    expect(readLock(stateDir(config))).toBeUndefined()
    expect(existsSync(socketPath(stateDir(config)))).toBe(false)
  }, 40_000)

  test("a second client does not start a second daemon", async () => {
    const config = project("d2", "services:\n  w:\n    cmd: sleep 300\n")
    const [a, b] = await Promise.all([open(config), open(config)])
    expect(a.hello.pid).toBe(b.hello.pid)
    const pid = a.hello.pid
    await a.dispose()
    await until(() => !alive(pid))
  }, 30_000)
})
