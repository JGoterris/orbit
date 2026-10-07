import { afterEach, describe, expect, test } from "bun:test"
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs"
import { homedir, tmpdir } from "node:os"
import { join } from "node:path"
import { loadConfig } from "../src/config/load.ts"
import {
  completePath,
  expandPath,
  findProject,
  forgetProject,
  looksLikePath,
  projectStatus,
  readProjects,
  registerProject,
  setPinned,
  sortProjects,
} from "../src/core/projects.ts"
import { Session } from "../src/core/session.ts"
import { acquireLock, readState, releaseLock } from "../src/core/state.ts"
import { Supervisor, type SupervisorLike } from "../src/core/supervisor.ts"
import { projectRows } from "../src/ui/Overlays.tsx"
import { sleepCmd, win } from "./helpers.ts"

process.env.XDG_STATE_HOME = mkdtempSync(`${tmpdir()}/orbit-state-`) // tests must not touch the real ~/.local/state
const freshConfigHome = () => (process.env.XDG_CONFIG_HOME = mkdtempSync(`${tmpdir()}/orbit-config-`))
freshConfigHome()

const tmp = () => mkdtempSync(join(tmpdir(), "orbit-proj-"))
function project(name: string, yaml = "services: {}\n") {
  const dir = join(tmp(), name)
  mkdirSync(dir)
  writeFileSync(join(dir, "orbit.yaml"), `name: ${name}\n${yaml}`)
  return dir
}

afterEach(freshConfigHome)

describe("project registry", () => {
  test("registers, keeps the pin when reopened, sorts pinned first then by recency", () => {
    registerProject("/a", "a", 100)
    registerProject("/b", "b", 200)
    registerProject("/c", "c", 300)
    expect(readProjects().map((p) => p.name)).toEqual(["c", "b", "a"])
    setPinned("/a", true)
    registerProject("/a", "a", 400) // reopening must not unpin it
    expect(sortProjects(readProjects()).map((p) => p.name)).toEqual(["a", "c", "b"])
    expect(readProjects().find((p) => p.path === "/a")).toEqual({ path: "/a", name: "a", lastOpened: 400, pinned: true })
    setPinned("/a", false)
    expect(readProjects().find((p) => p.path === "/a")?.pinned).toBeUndefined()
  })

  test("forget removes it, and old unpinned entries are dropped beyond the cap but pinned ones stay", () => {
    registerProject("/keep", "keep", 1)
    setPinned("/keep", true)
    for (let i = 0; i < 40; i++) registerProject(`/p${i}`, `p${i}`, 100 + i)
    const list = readProjects()
    expect(list.filter((p) => !p.pinned)).toHaveLength(30)
    expect(list.some((p) => p.path === "/keep")).toBe(true)
    expect(list.some((p) => p.path === "/p0")).toBe(false)
    expect(list.some((p) => p.path === "/p39")).toBe(true)
    forgetProject("/p39")
    expect(readProjects().some((p) => p.path === "/p39")).toBe(false)
  })

  test("a missing or corrupt projects.json reads as empty and bad entries are skipped", () => {
    expect(readProjects()).toEqual([])
    const dir = join(process.env.XDG_CONFIG_HOME!, "orbit")
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, "projects.json"), "{nope")
    expect(readProjects()).toEqual([])
    writeFileSync(join(dir, "projects.json"), JSON.stringify({ projects: [null, { path: 3 }, { path: "/ok", name: "ok" }] }))
    expect(readProjects()).toEqual([{ path: "/ok", name: "ok", lastOpened: 0 }])
  })
})

describe.skipIf(win)("paths", () => {
  test("looksLikePath and expandPath", () => {
    for (const p of ["/x", "~", "~/x", "./x", "../x", ".."]) expect(looksLikePath(p)).toBe(true)
    for (const p of ["orbit", "my-api", ".hidden"]) expect(looksLikePath(p)).toBe(false)
    expect(expandPath("~/code")).toBe(join(homedir(), "code"))
    expect(expandPath("x", "/base")).toBe("/base/x")
  })

  test("completePath: single match gets a slash, several complete to the common prefix, hidden dirs only on demand", () => {
    const root = tmp()
    for (const d of ["alpha", "alpine", "beta", ".git"]) mkdirSync(join(root, d))
    writeFileSync(join(root, "alfile"), "")
    expect(completePath(`${root}/be`)).toBe(`${root}/beta/`)
    expect(completePath(`${root}/al`)).toBe(`${root}/alp`) // alpha + alpine; the file "alfile" is not a folder
    expect(completePath(`${root}/zzz`)).toBe(`${root}/zzz`)
    expect(completePath(`${root}/.g`)).toBe(`${root}/.git/`)
    expect(completePath(`${root}/`)).toBe(`${root}/`) // alpha, alpine, beta: nothing in common
    expect(completePath(`${root}/nope/x`)).toBe(`${root}/nope/x`)
    expect(completePath("rel", root)).toBe("rel") // nothing named rel*
    mkdirSync(join(root, "relative"))
    expect(completePath("rel", root)).toBe("relative/")
    expect(completePath("./rel", root)).toBe("./relative/")
  })
})

describe("projectStatus", () => {
  test("reports a missing folder, and the orbit that has the project open", () => {
    const dir = project("stat")
    const cfg = loadConfig({ dir })
    const entry = { path: dir, name: cfg.name, lastOpened: 1 }
    expect(projectStatus(entry)).toEqual({ exists: true, openIn: undefined, running: 0 })
    expect(projectStatus({ ...entry, path: "/definitely/not/here" }).exists).toBe(false)

    // another orbit (pid 1 is always alive) holds the lock
    const sup = new Supervisor(cfg)
    acquireLock(sup.stateDir)
    expect(projectStatus(entry).openIn).toBeUndefined() // that is us
    releaseLock(sup.stateDir)
  })

  test("counts processes a previous session left running", async () => {
    const dir = project("left", `services:\n  s:\n    cmd: ${sleepCmd(30)}\n`)
    const sup = new Supervisor(loadConfig({ dir }))
    await sup.init()
    expect(await sup.start("s")).toBe(true)
    sup.detach()
    const pid = readState(sup.stateDir).services.s!.pid!
    try {
      expect(projectStatus({ path: dir, name: "left", lastOpened: 1 }).running).toBe(1)
    } finally {
      process.kill(-pid, "SIGKILL")
    }
    await Bun.sleep(100)
    expect(projectStatus({ path: dir, name: "left", lastOpened: 1 }).running).toBe(0)
  })
})

describe("projectRows", () => {
  const entries = [
    { path: "/w/api", name: "api", lastOpened: 1 },
    { path: "/w/web", name: "web", lastOpened: 3, pinned: true },
    { path: "/w/shop", name: "shop", lastOpened: 2 },
  ]
  const statuses = new Map([["/w/shop", { exists: true, running: 2 }]])

  test("pinned first then recent, with status flags, filtered fuzzily", () => {
    const rows = projectRows(entries, statuses, "", "/w/api")
    expect(rows.map((r) => r.path)).toEqual(["/w/web", "/w/shop", "/w/api"])
    expect(rows[0]!.label).toStartWith("★ web")
    expect(rows[1]!.hint).toBe("● 2 up")
    expect(rows[2]!.hint).toBe("current")
    expect(projectRows(entries, statuses, "sh", "/w/api").map((r) => r.path)).toEqual(["/w/shop"])
  })

  test.skipIf(win)("a typed path becomes an 'open folder' row, flagged when it is not a folder", () => {
    const real = tmp()
    expect(projectRows(entries, statuses, "zzz", "/w/api")).toEqual([]) // not path-like: a search, nothing matches
    const rows = projectRows(entries, statuses, `${real}/`, "/w/api")
    expect(rows[0]).toMatchObject({ path: real, missing: false })
    const bad = projectRows(entries, statuses, "/no/such/dir", "/w/api")
    expect(bad[0]).toMatchObject({ path: "/no/such/dir", missing: true, hint: "not a folder" })
  })

  test("a remembered project whose folder is gone is marked missing", () => {
    const rows = projectRows(entries, new Map([["/w/api", { exists: false, running: 0 }]]), "api", "/w/web")
    expect(rows[0]).toMatchObject({ path: "/w/api", missing: true })
    expect(rows[0]!.hint).toBe("missing")
  })
})

describe("Session.switchTo", () => {
  async function open(dir: string) {
    const sup = new Supervisor(loadConfig({ dir }))
    expect(acquireLock(sup.stateDir)).toBeUndefined()
    await sup.init()
    return new Session(sup)
  }

  test("switches, registers the new project, hands over the lock, and stops the old services", async () => {
    const a = project("one", `services:\n  s:\n    cmd: ${sleepCmd(30)}\n`)
    const b = project("two")
    const session = await open(a)
    const first = session.sup
    expect(await first.start("s")).toBe(true)
    const pid = first.state("s").pid!

    let seen: SupervisorLike | undefined
    expect(await session.switchTo(b, "stop", (s) => (seen = s))).toBeUndefined()
    expect(session.sup).not.toBe(first)
    expect(seen).toBe(session.sup)
    expect(session.sup.config.name).toBe("two")
    expect(readProjects().map((p) => p.path)).toEqual([b])
    expect(() => process.kill(pid, 0)).toThrow() // the old service was stopped
    // the old project's lock is free again, the new one is ours
    expect(acquireLock(first.stateDir)).toBeUndefined()
    releaseLock(first.stateDir)
    releaseLock(session.sup.stateDir)
  })

  test("detach leaves the old services running and recorded for the next time", async () => {
    const a = project("keep", `services:\n  s:\n    cmd: ${sleepCmd(30)}\n`)
    const b = project("other")
    const session = await open(a)
    const old = session.sup
    expect(await old.start("s")).toBe(true)
    expect(await session.switchTo(b, "detach")).toBeUndefined()
    const pid = readState(old.stateDir).services.s!.pid!
    try {
      expect(() => process.kill(pid, 0)).not.toThrow()
      expect(projectStatus({ path: a, name: "keep", lastOpened: 1 }).running).toBe(1)
      // ...and coming back picks it up again
      expect(await session.switchTo(a, "stop")).toBeUndefined()
      await Bun.sleep(300)
      expect(session.sup.isUp("s")).toBe(true)
      expect(session.sup.logs.lines("s").some((l) => l.text.includes("re-attached to running process"))).toBe(true)
    } finally {
      try {
        process.kill(-pid, "SIGKILL")
      } catch {}
      releaseLock(session.sup.stateDir)
    }
  })

  test("errors leave the current project untouched", async () => {
    const a = project("stay", `services:\n  s:\n    cmd: ${sleepCmd(30)}\n`)
    const session = await open(a)
    const sup = session.sup
    expect(await sup.start("s")).toBe(true)
    try {
      expect(await session.switchTo(a, "stop")).toContain("already open")
      expect(await session.switchTo(join(tmp(), "missing-dir"), "stop")).toContain("is not a folder")
      expect(await session.switchTo(join(a, "orbit.yaml"), "stop")).toContain("is not a folder")
      const broken = tmp()
      writeFileSync(join(broken, "orbit.yaml"), "services: [oops")
      expect(await session.switchTo(broken, "stop")).toContain("invalid YAML")

      // another live orbit has the target open: pid 1 is always alive
      const taken = project("taken")
      const probe = new Supervisor(loadConfig({ dir: taken }))
      const { mkdirSync: mk, writeFileSync: wf } = await import("node:fs")
      mk(probe.stateDir, { recursive: true })
      wf(join(probe.stateDir, "orbit.lock"), "1")
      expect(await session.switchTo(taken, "stop")).toContain("pid 1")

      expect(session.sup).toBe(sup)
      expect(sup.isUp("s")).toBe(true)
    } finally {
      await sup.dispose()
      releaseLock(sup.stateDir)
    }
  })
})

describe("findProject / CLI", () => {
  const list = [
    { path: "/w/shop-api", name: "shop-api", lastOpened: 1 },
    { path: "/w/shop-web", name: "shop-web", lastOpened: 2 },
    { path: "/x/blog", name: "blog", lastOpened: 3 },
    { path: "/y/blog", name: "blog", lastOpened: 4 },
    { path: "/z/docs", name: "docs", lastOpened: 5 },
  ]

  test.skipIf(win)("exact name, path, then unique fragment; ambiguity and misses explain themselves", () => {
    expect(findProject("docs", list)).toMatchObject({ path: "/z/docs" })
    expect(findProject("/w/shop-web", list)).toMatchObject({ name: "shop-web" })
    expect(findProject("DOC", list)).toMatchObject({ path: "/z/docs" })
    expect(findProject("blog", list)).toContain("/x/blog, /y/blog")
    expect(findProject("shop", list)).toContain("shop-api, shop-web")
    expect(findProject("nothing", list)).toContain("no project")
    expect(findProject("x", [])).toContain("orbit projects")
  })

  const orbit = (args: string[], home: string) =>
    Bun.spawnSync(["bun", `${import.meta.dir}/../src/index.tsx`, ...args], {
      env: { ...process.env, XDG_CONFIG_HOME: home, XDG_STATE_HOME: process.env.XDG_STATE_HOME, NO_COLOR: "1" },
      stdin: "ignore",
    })

  test("`orbit projects` lists them and `orbit open` explains what it could not find", () => {
    const home = mkdtempSync(`${tmpdir()}/orbit-cfg-`)
    expect(orbit(["projects"], home).stdout.toString()).toContain("no projects yet")

    const dir = project("listed")
    const prev = process.env.XDG_CONFIG_HOME
    process.env.XDG_CONFIG_HOME = home
    registerProject(dir, "listed", 1)
    setPinned(dir, true)
    process.env.XDG_CONFIG_HOME = prev
    const out = orbit(["projects"], home).stdout.toString()
    expect(out).toContain("★ listed")
    expect(out).toContain(dir)

    const miss = orbit(["open", "nope"], home)
    expect(miss.exitCode).toBe(1)
    expect(miss.stderr.toString()).toContain('no project "nope"')
    const usage = orbit(["open"], home)
    expect(usage.exitCode).toBe(1)
    expect(usage.stderr.toString()).toContain("usage: orbit open")
  })
})
