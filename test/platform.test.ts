import { afterEach, describe, expect, test } from "bun:test"
import { mkdtempSync, readFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import type { ServiceConfig } from "../src/config/schema.ts"
import { shellCommand, parsePsTable, parsePsTime, parseWinTable, configBase, isWindows, killTree, pidAlive, procStartTime, processTable, shellArgv, stateBase, treeAlive, whoListens } from "../src/core/platform/index.ts"
import { ProcessSampler } from "../src/core/metrics.ts"
import { ProcessRunner } from "../src/core/runners.ts"
import { procFiles } from "../src/core/state.ts"
import { sleepCmd } from "./helpers.ts"

const until = async (cond: () => boolean, ms = 5000) => {
  const end = Date.now() + ms
  while (!cond() && Date.now() < end) await Bun.sleep(25)
  return cond()
}

describe("shellArgv", () => {
  test("sh -c on POSIX, cmd /c on Windows", () => {
    expect(shellArgv("echo hi", undefined, false)).toEqual(["/bin/sh", "-c", "echo hi"])
    const win = shellArgv("echo hi", undefined, true)
    expect(win.slice(1)).toEqual(["/d", "/s", "/c", "echo hi"])
    expect(win[0]).toMatch(/cmd(\.exe)?$/i)
  })

  test("cmd gets its command pre-quoted and verbatim; other shells are left to Bun", () => {
    const c = shellCommand('bun -e "x()"', undefined, true)
    expect(c.verbatim).toBe(true)
    expect(c.argv.slice(1)).toEqual(["/d", "/s", "/c", '"bun -e "x()""'])
    expect(shellCommand("ls", "pwsh", true).verbatim).toBe(false)
    expect(shellCommand("ls", undefined, false)).toEqual({ argv: ["/bin/sh", "-c", "ls"], verbatim: false })
  })

  test("`shell` picks the flags that suit it", () => {
    expect(shellArgv("ls", "pwsh")).toEqual(["pwsh", "-NoProfile", "-Command", "ls"])
    expect(shellArgv("ls", "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe")).toEqual([
      "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe", "-NoProfile", "-Command", "ls",
    ])
    expect(shellArgv("dir", "cmd.exe")).toEqual(["cmd.exe", "/d", "/s", "/c", "dir"])
    expect(shellArgv("ls", "/usr/bin/bash")).toEqual(["/usr/bin/bash", "-c", "ls"])
  })
})

describe("process table parsers", () => {
  test("ps cpu time", () => {
    expect(parsePsTime("0:01.50")).toBeCloseTo(1.5)
    expect(parsePsTime("12:30.00")).toBeCloseTo(750)
    expect(parsePsTime("1:02:03")).toBe(3723)
    expect(parsePsTime("2-00:00:01")).toBe(172801)
    expect(parsePsTime("nonsense")).toBe(0)
  })

  test("ps rows: pid ppid rss(KiB) time", () => {
    const t = parsePsTable("    1     0  1024  0:00.10\n  200     1  2048  1:00.00\nbad line\n")
    expect(t.get(200)).toEqual({ ppid: 1, cpuSeconds: 60, rss: 2048 * 1024 })
    expect(t.size).toBe(2)
  })

  test("Win32_Process json (a single process is an object, not a list)", () => {
    const row = { ProcessId: 10, ParentProcessId: 4, WorkingSetSize: 4096, KernelModeTime: 10_000_000, UserModeTime: 20_000_000 }
    expect(parseWinTable(JSON.stringify([row])).get(10)).toEqual({ ppid: 4, cpuSeconds: 3, rss: 4096 })
    expect(parseWinTable(JSON.stringify(row)).get(10)?.rss).toBe(4096)
    expect(parseWinTable("not json").size).toBe(0)
  })
})

describe("per-user directories", () => {
  test("XDG variables win on every platform", () => {
    const saved = { s: process.env.XDG_STATE_HOME, c: process.env.XDG_CONFIG_HOME }
    process.env.XDG_STATE_HOME = "/x/state"
    process.env.XDG_CONFIG_HOME = "/x/config"
    try {
      expect(stateBase()).toBe("/x/state")
      expect(configBase()).toBe("/x/config")
    } finally {
      for (const [k, v] of [["XDG_STATE_HOME", saved.s], ["XDG_CONFIG_HOME", saved.c]] as const) {
        if (v === undefined) delete process.env[k]
        else process.env[k] = v
      }
    }
  })
})

describe("live process queries", () => {
  test("pidAlive / treeAlive / procStartTime on ourselves", () => {
    expect(pidAlive(process.pid)).toBe(true)
    const t = procStartTime(process.pid)
    expect(typeof t).toBe("number")
    expect(procStartTime(process.pid)).toBe(t!) // stable
  })

  test("processTable and ProcessSampler see this process", async () => {
    const table = await processTable()
    expect(table.get(process.pid)?.rss).toBeGreaterThan(0)
    const sampler = new ProcessSampler()
    await sampler.sample([process.pid])
    const s = (await sampler.sample([process.pid])).get(process.pid)
    expect(s?.mem).toBeGreaterThan(0)
  })

  test("whoListens names the owner of a port", async () => {
    const server = Bun.listen({ hostname: "127.0.0.1", port: 0, socket: { data() {} } })
    try {
      const who = await whoListens(server.port)
      // `ss` / `lsof` / `netstat` may be missing in a minimal container: only check what they say when they answer
      if (who) expect(who).toContain(String(process.pid))
    } finally {
      server.stop(true)
    }
  })
})

describe("killTree", () => {
  const pids: number[] = []
  afterEach(() => {
    for (const p of pids.splice(0)) killTree(p, "SIGKILL")
  })

  test("takes the children down with the parent", async () => {
    const script = `const c = Bun.spawn(["bun","-e","setTimeout(()=>{},60000)"],{stdio:["ignore","ignore","ignore"]}); console.log(c.pid); setTimeout(()=>{},60000)`
    const parent = Bun.spawn(["bun", "-e", script], { stdin: "ignore", stdout: "pipe", stderr: "ignore", detached: true })
    pids.push(parent.pid)
    const reader = parent.stdout.getReader()
    const childPid = Number(new TextDecoder().decode((await reader.read()).value).trim())
    expect(pidAlive(childPid)).toBe(true)
    expect(treeAlive(parent.pid)).toBe(true)
    killTree(parent.pid, "SIGKILL")
    await parent.exited
    expect(await until(() => !pidAlive(childPid))).toBe(true)
  })
})

describe("ProcessRunner", () => {
  const dir = mkdtempSync(join(tmpdir(), "orbit-runner-"))
  const svc = (cmd: string) => ({ name: "p", type: "process", cmd, cwd: dir, env: {}, envFiles: [] }) as unknown as ServiceConfig

  test("starts, reports its pid and stops", async () => {
    let exit: { code: number | null } | undefined
    const runner = new ProcessRunner(svc(sleepCmd(60)), { log() {}, exit: (code) => (exit = { code }) }, procFiles(join(dir, "a"), "p"))
    await runner.start()
    expect(runner.pid).toBeGreaterThan(0)
    expect(pidAlive(runner.pid!)).toBe(true)
    await runner.stop(3000)
    expect(await until(() => exit !== undefined)).toBe(true)
    expect(await until(() => !pidAlive(runner.pid!))).toBe(true)
  })

  test("keeps the exit code of a command that ends on its own", async () => {
    let exit: { code: number | null } | undefined
    const files = procFiles(join(dir, "b"), "p")
    const runner = new ProcessRunner(svc(`bun -e "console.log('hi'); process.exit(3)"`), { log() {}, exit: (code) => (exit = { code }) }, files)
    await runner.start()
    expect(await until(() => exit !== undefined)).toBe(true)
    expect(exit!.code).toBe(3)
    expect(readFileSync(files.exit, "utf8").trim()).toBe("3")
  })

  test("a process of ours is not a recycled pid", () => {
    expect(isWindows || procStartTime(process.pid) !== undefined).toBe(true)
  })
})
