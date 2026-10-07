import { describe, expect, test } from "bun:test"
import { EventEmitter } from "node:events"
import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { attachDesktopNotifier, pickBackend, powershellScript, type Notice } from "../src/core/desktopNotify.ts"
import { readUserConfig } from "../src/core/userConfig.ts"

class FakeSup extends EventEmitter {
  config = { name: "shop" }
  names = ["api", "web", "db"]
  private status: Record<string, { status: string; error?: string; health?: string }> = {
    api: { status: "healthy" },
    web: { status: "healthy" },
    db: { status: "healthy" },
  }
  state(name: string) {
    return this.status[name]!
  }
  set(name: string, status: string, extra: { error?: string; health?: string } = {}) {
    this.status[name] = { status, ...extra }
    this.emit("change", name)
  }
}

function setup(opts: { enabled?: () => boolean; throttleMs?: number } = {}) {
  const sup = new FakeSup()
  const sent: Notice[] = []
  let t = 0
  const stop = attachDesktopNotifier(sup as never, { send: (n) => sent.push(n), groupMs: 10, now: () => t, ...opts })
  return { sup, sent, stop, advance: (ms: number) => (t += ms) }
}
const settle = () => Bun.sleep(30)

describe("desktop notifier", () => {
  test("crash, unhealthy and recovery", async () => {
    const { sup, sent } = setup()
    sup.set("api", "crashed", { error: "exited with code 1" })
    await settle()
    expect(sent).toEqual([{ title: "orbit · shop", body: "✖  api crashed: exited with code 1", urgent: true }])
    sup.set("web", "unhealthy", { health: "HTTP 503" })
    await settle()
    expect(sent[1]!.body).toBe("●  web is unhealthy: HTTP 503")
    sup.set("web", "healthy")
    await settle()
    expect(sent[2]).toEqual({ title: "orbit · shop", body: "✔  web recovered", urgent: false })
  })

  test("a service that never went down does not 'recover'; stopping is not a failure", async () => {
    const { sup, sent } = setup()
    sup.set("api", "starting")
    sup.set("api", "healthy")
    sup.set("db", "stopping")
    sup.set("db", "stopped")
    await settle()
    expect(sent).toEqual([])
  })

  test("a cascade is a single notification", async () => {
    const { sup, sent } = setup()
    sup.set("db", "crashed", { error: "exited with code 1" })
    sup.set("api", "failed", { error: "dependency not ready: db" })
    sup.set("web", "failed", { error: "dependency not ready: db" })
    await settle()
    expect(sent).toHaveLength(1)
    expect(sent[0]!.body).toBe("✖  3 services down: db, api, web")
  })

  test("a restart loop is reported once per minute", async () => {
    const { sup, sent, advance } = setup()
    for (let i = 0; i < 3; i++) {
      sup.set("api", "starting")
      sup.set("api", "crashed", { error: "boom" })
      advance(5_000)
    }
    await settle()
    expect(sent).toHaveLength(1)
    advance(60_000)
    sup.set("api", "starting")
    sup.set("api", "crashed", { error: "boom" })
    await settle()
    expect(sent).toHaveLength(2)
  })

  test("disabled: nothing is sent; dispose detaches", async () => {
    const off = setup({ enabled: () => false })
    off.sup.set("api", "crashed")
    await settle()
    expect(off.sent).toEqual([])
    const gone = setup()
    gone.stop()
    gone.sup.set("api", "crashed")
    await settle()
    expect(gone.sent).toEqual([])
  })
})

describe("backends", () => {
  const which = (...found: string[]) => (cmd: string) => (found.includes(cmd) ? `/bin/${cmd}` : null)
  const n: Notice = { title: "orbit · it's", body: "✖  api crashed: -x ‘quoted’", urgent: true }

  test("picks the Windows side on WSL, osascript on macOS, notify-send on Linux", () => {
    expect(pickBackend({ platform: "linux", wsl: true, which: which("powershell.exe", "notify-send") })?.name).toBe("powershell")
    expect(pickBackend({ platform: "linux", wsl: true, which: which("notify-send") })?.name).toBe("notify-send")
    expect(pickBackend({ platform: "darwin", wsl: false, which: which("osascript") })?.name).toBe("osascript")
    expect(pickBackend({ platform: "linux", wsl: false, which: which("notify-send") })?.name).toBe("notify-send")
    expect(pickBackend({ platform: "linux", wsl: false, which: which() })).toBeUndefined()
  })

  test("texts reach the program as data, not as code", () => {
    const ns = pickBackend({ platform: "linux", wsl: false, which: which("notify-send") })!.argv(n)
    expect(ns).toEqual(["/bin/notify-send", "-a", "orbit", "-u", "critical", "--", n.title, n.body])
    const mac = pickBackend({ platform: "darwin", wsl: false, which: which("osascript") })!.argv(n)
    expect(mac.slice(-2)).toEqual([n.title, n.body])
    // only the -e arguments are script: the texts are never part of them
    expect(mac.filter((_, i) => mac[i - 1] === "-e").join("\n")).not.toContain("crashed")
  })

  test("powershell: quotes cannot end the string", () => {
    const script = powershellScript(n)
    expect(script).toContain("InnerText = 'orbit · it''s'")
    expect(script).toContain("-x ''quoted''")
    const argv = pickBackend({ platform: "linux", wsl: true, which: which("powershell.exe") })!.argv(n)
    expect(Buffer.from(argv.at(-1)!, "base64").toString("utf16le")).toBe(script)
  })
})

describe("user config", () => {
  test("notifications survives reading and a theme write", async () => {
    process.env.XDG_CONFIG_HOME = mkdtempSync(join(tmpdir(), "orbit-cfg-"))
    const { mkdirSync } = await import("node:fs")
    mkdirSync(join(process.env.XDG_CONFIG_HOME, "orbit"))
    writeFileSync(join(process.env.XDG_CONFIG_HOME, "orbit", "config.json"), JSON.stringify({ theme: "x", notifications: false }))
    expect(readUserConfig()).toEqual({ theme: "x", notifications: false })
    const { writeUserConfig } = await import("../src/core/userConfig.ts")
    writeUserConfig({ theme: "y" })
    expect(readUserConfig()).toEqual({ theme: "y", notifications: false })
  })
})

describe("desktop notifier: memory", () => {
  class ResSup extends FakeSup {
    res: Record<string, unknown> = {}
    state(name: string) {
      return { ...super.state(name), resources: this.res[name] } as never
    }
    setRes(name: string, r: unknown) {
      this.res[name] = r
      this.emit("change", name)
    }
  }
  const make = () => {
    const sup = new ResSup()
    const sent: Notice[] = []
    let t = 0
    attachDesktopNotifier(sup as never, { send: (n) => sent.push(n), groupMs: 10, now: () => t, enabled: () => true })
    return { sup, sent, advance: (ms: number) => (t += ms) }
  }
  const MB = 1024 ** 2

  test("going over the limit is urgent and reported once; warn is silent", async () => {
    const { sup, sent } = make()
    sup.setRes("api", { memLimit: 1024 * MB, level: "warn" })
    await settle()
    expect(sent).toEqual([])
    sup.setRes("api", { memLimit: 1024 * MB, level: "over" })
    sup.setRes("api", { memLimit: 1024 * MB, level: "over" })
    await settle()
    expect(sent).toEqual([{ title: "orbit · shop", body: "▲  api is over its memory limit (limit 1.0G)", urgent: true }])
  })

  test("a leak is reported once with its rate, and again only after the long throttle", async () => {
    const { sup, sent, advance } = make()
    const leak = { perMin: 5 * MB, since: 0, etaMs: 40 * 60_000 }
    sup.setRes("web", { leak })
    sup.setRes("web", { leak })
    await settle()
    expect(sent.length).toBe(1)
    expect(sent[0]).toMatchObject({ urgent: false })
    expect(sent[0]!.body).toBe("↗  web may be leaking memory: +5.0M/min, limit in ~40m00s")
    sup.setRes("web", undefined)
    advance(5 * 60_000)
    sup.setRes("web", { leak })
    await settle()
    expect(sent.length).toBe(1)
    sup.setRes("web", undefined)
    advance(11 * 60_000)
    sup.setRes("web", { leak })
    await settle()
    expect(sent.length).toBe(2)
  })
})
