import { afterEach, beforeEach, describe, expect, test } from "bun:test"
import { testRender } from "@opentui/react/test-utils"
import { loadConfig } from "../src/config/load.ts"
import { Supervisor } from "../src/core/supervisor.ts"
import { App } from "../src/ui/App.tsx"
import { clipboard } from "../src/ui/clipboard.ts"
import { readUserConfig } from "../src/core/userConfig.ts"
import { applyTheme, theme } from "../src/ui/theme.ts"
import { THEMES } from "../src/ui/themes.ts"
import { mkdirSync, mkdtempSync, mkdtempSync as __mk, writeFileSync } from "node:fs"
import { tmpdir, tmpdir as __tmp } from "node:os"
import { join } from "node:path"
import { readProjects, registerProject, setPinned } from "../src/core/projects.ts"
process.env.XDG_STATE_HOME = __mk(`${__tmp()}/orbit-state-`) // tests must not touch the real ~/.local/state
process.env.XDG_CONFIG_HOME = __mk(`${__tmp()}/orbit-config-`) // ...nor the real ~/.config

// The app has live timers (spinners, clocks), so rendering is driven with renderOnce() + short
// waits instead of act(); testRender() turns the act environment on, so switch it off again.
const noActEnvironment = () => ((globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = false)
const act = async (fn: () => Promise<void>) => fn()

let cleanup: (() => void) | undefined
afterEach(() => {
  cleanup?.()
  applyTheme(THEMES.orbit!)
})

async function setup() {
  const config = loadConfig({ dir: `${import.meta.dir}/fixtures/stack` })
  const sup = new Supervisor(config)
  const t = await testRender(<App sup={sup} onQuit={() => {}} />, { width: 150, height: 40 })
  cleanup = () => t.renderer.destroy()
  noActEnvironment()
  await t.renderOnce()
  return { ...t, sup }
}

async function press(t: Pick<Awaited<ReturnType<typeof setup>>, "mockInput" | "renderOnce">, key: string) {
  await act(async () => {
    t.mockInput.pressKey(key)
    await Bun.sleep(30)
  })
  await t.renderOnce()
}

describe("tui", () => {
  test("dashboard, graph, palette, help", async () => {
    const t = await setup()
    let frame = t.captureCharFrame()
    if (process.env.SHOW) console.log(frame)
    expect(frame).toContain("orbit")
    expect(frame).toContain("Services 0/8")
    expect(frame).toContain("postgres")

    await press(t, "2")
    frame = t.captureCharFrame()
    if (process.env.SHOW) console.log(frame)
    expect(frame).toContain("Dependency graph")
    expect(frame).toContain("▶")

    await press(t, "j")
    await press(t, "3")
    frame = t.captureCharFrame()
    if (process.env.SHOW) console.log(frame)
    expect(frame).toContain("Logs · all services")

    await press(t, ":")
    frame = t.captureCharFrame()
    if (process.env.SHOW) console.log(frame)
    expect(frame).toContain("Command palette")
    expect(frame).toContain("Start all services")
    await act(async () => {
      await t.mockInput.typeText("restart all")
      await Bun.sleep(30)
    })
    await t.renderOnce()
    frame = t.captureCharFrame()
    expect(frame).toContain("Restart all running services")
    expect(frame).not.toContain("View: Graph")
    await press(t, "ESCAPE")
    await Bun.sleep(100)
    await t.renderOnce()
    expect(t.captureCharFrame()).not.toContain("Command palette")

    await press(t, "?")
    frame = t.captureCharFrame()
    if (process.env.SHOW) console.log(frame)
    expect(frame).toContain("command palette")
  })

  test("tab moves focus between panels and z zooms the focused one", async () => {
    const t = await setup()
    const frame = () => t.captureCharFrame()
    expect(frame()).toContain("Services 0/8")

    // dashboard: services -> detail -> logs
    await press(t, "TAB")
    await press(t, "z")
    expect(frame()).not.toContain("Services 0/8")
    expect(frame()).toContain("zoom")
    expect(frame()).toContain("needs")
    await press(t, "z")
    expect(frame()).toContain("Services 0/8")

    await press(t, "TAB")
    await press(t, "z")
    expect(frame()).toContain("Logs · ")
    expect(frame()).not.toContain("Services 0/8")
    expect(frame()).not.toContain("needs")
    await press(t, "ESCAPE")
    await Bun.sleep(100)
    await t.renderOnce()
    expect(frame()).toContain("Services 0/8")

    // logs view: j/k scroll instead of changing the selected service
    await press(t, "3")
    await press(t, "z")
    expect(frame()).toContain("Logs · all services")
    expect(frame()).not.toContain("Services 0/8")
    await press(t, "j")
    await press(t, "z")
    expect(frame()).toContain("Services 0/8")
    expect(frame()).toContain("│▌○ postgres")
  })

  test("+ / - / = resize the focused panel", async () => {
    const t = await setup()
    const lines = () => t.captureCharFrame().split("\n")
    const sidebarW = () => lines()[1]!.indexOf("╮") + 1
    const rowOf = (s: string) => lines().findIndex((l) => l.includes(s))

    const w0 = sidebarW()
    await press(t, "+")
    expect(sidebarW()).toBe(w0 + 2)
    await press(t, "=")
    expect(sidebarW()).toBe(w0)
    for (let i = 0; i < 30; i++) await press(t, "-")
    expect(sidebarW()).toBe(16)

    // detail: shrinking it hides the "needs" line
    await press(t, "=")
    await press(t, "TAB")
    expect(rowOf("needs")).toBeGreaterThan(0)
    for (let i = 0; i < 6; i++) await press(t, "-")
    expect(rowOf("needs")).toBe(-1)
    await press(t, "=")
    expect(rowOf("needs")).toBeGreaterThan(0)

    // logs (dashboard): growing the panel moves its top edge up
    await press(t, "TAB")
    const top = rowOf("Logs · ")
    await press(t, "+")
    expect(rowOf("Logs · ")).toBe(top - 1)

    // zoom ignores resizing
    await press(t, "z")
    await press(t, "+")
    await press(t, "z")
    expect(rowOf("Logs · ")).toBe(top - 1)
  })

  test("detail shows config, charts and recent events when it has room; e opens the env overlay", async () => {
    const t = await setup()
    const text = () => t.captureCharFrame()
    await press(t, "TAB")
    expect(text()).not.toContain("start_timeout")
    await press(t, "z")
    expect(text()).toContain("start_timeout")
    expect(text()).toContain("recent")
    await press(t, "z")
    for (let i = 0; i < 8; i++) await press(t, "+")
    expect(text()).toContain("start_timeout")

    await press(t, "e")
    expect(text()).toContain("Env · ")
    expect(text()).toContain("LOG_LEVEL")
    await press(t, "ESCAPE")
    expect(text()).not.toContain("Env · ")
  })

  test("mouse: click the header tabs to switch views", async () => {
    const t = await setup()
    const click = async (label: string) => {
      const x = t.captureCharFrame().split("\n")[0]!.indexOf(label) + 1
      await act(async () => {
        await t.mockMouse.click(x, 0)
        await Bun.sleep(30)
      })
      await t.renderOnce()
    }
    await click("2 Graph")
    expect(t.captureCharFrame()).toContain("Dependency graph")
    await click("3 Logs")
    expect(t.captureCharFrame()).toContain("Logs · all services")
    await click("1 Dashboard")
    expect(t.captureCharFrame()).toContain("Services 0/8")
    expect(t.captureCharFrame()).toContain("needs")
  })

  test("mouse: click a service row and a graph node to select them", async () => {
    const t = await setup()
    const rowOf = (name: string) => t.captureCharFrame().split("\n").findIndex((l) => l.startsWith(`│ ○ ${name} `))
    await act(async () => {
      await t.mockMouse.click(6, rowOf("worker"))
      await Bun.sleep(30)
    })
    await t.renderOnce()
    expect(t.captureCharFrame()).toContain("─ worker ─")

    await press(t, "2")
    const lines = t.captureCharFrame().split("\n")
    const y = lines.findIndex((l) => l.includes("│ ○ web "))
    const x = lines[y]!.indexOf("│ ○ web ") + 4
    await act(async () => {
      await t.mockMouse.click(x, y)
      await Bun.sleep(30)
    })
    await t.renderOnce()
    // the selected node gets a double border
    expect(t.captureCharFrame()).toContain("║ ○ web")
  })

  test("starting a service from the UI shows it healthy, with logs", async () => {
    const port = 41000 + Math.floor(Math.random() * 10000)
    const { mkdtempSync, writeFileSync } = await import("node:fs")
    const { tmpdir } = await import("node:os")
    const dir = mkdtempSync(`${tmpdir()}/orbit-ui-`)
    writeFileSync(
      `${dir}/orbit.yaml`,
      `name: ui\ncompose: false\nservices:\n  base:\n    cmd: echo base ready; sleep 30\n  srv:\n    cmd: bun -e 'console.log("hello from srv"); Bun.serve({port:${port},fetch:()=>new Response("ok")})'\n    port: ${port}\n    depends_on: [base]\n`,
    )
    const sup = new Supervisor(loadConfig({ dir }))
    const t = await testRender(<App sup={sup} onQuit={() => {}} />, { width: 140, height: 30 })
    cleanup = () => t.renderer.destroy()
    noActEnvironment()
    await t.renderOnce()
    await press(t, "j") // select srv
    await press(t, " ") // start it (and base first)
    await act(async () => {
      for (let i = 0; i < 60 && sup.state("srv").status !== "healthy"; i++) await Bun.sleep(100)
      await Bun.sleep(100)
    })
    await t.renderOnce()
    const frame = t.captureCharFrame()
    if (process.env.SHOW) console.log(frame)
    expect(sup.state("base").status).toBe("running")
    expect(sup.state("srv").status).toBe("healthy")
    expect(frame).toContain("Services 2/2")
    expect(frame).toContain("healthy")
    expect(frame).toContain("hello from srv")
    await act(async () => {
      await sup.dispose()
    })
  }, 15000)

  test("logs: long lines wrap (w toggles), and the view stays put while scrolled up", async () => {
    const t = await setup()
    const svc = t.sup.names[0]!
    await press(t, "3")
    t.sup.logs.append(svc, "stdout", `${"x".repeat(250)} TAIL-MARK`)
    await Bun.sleep(80)
    await t.renderOnce()
    expect(t.captureCharFrame()).toContain("TAIL-MARK")
    await press(t, "w")
    expect(t.captureCharFrame()).not.toContain("TAIL-MARK")
    await press(t, "w")

    for (let i = 1; i <= 80; i++) t.sup.logs.append(svc, "stdout", `line-${String(i).padStart(3, "0")}`)
    await Bun.sleep(80)
    await t.renderOnce()
    await press(t, "\x1b[5~") // page up
    const seen = () => new Set(t.captureCharFrame().match(/line-\d{3}/g))
    const before = seen()
    expect(before.size).toBeGreaterThan(5)
    expect(before.has("line-080")).toBe(false)
    for (let i = 81; i <= 100; i++) t.sup.logs.append(svc, "stdout", `line-${String(i).padStart(3, "0")}`)
    await Bun.sleep(150)
    await t.renderOnce()
    await t.renderOnce()
    expect(seen()).toEqual(before)
    expect(t.captureCharFrame()).toContain("new")
  })

  test("logs: copy mode selects a range with the keyboard and copies full lines", async () => {
    const t = await setup()
    const svc = t.sup.names[0]!
    for (let i = 1; i <= 5; i++) t.sup.logs.append(svc, "stdout", `row-${i} ${"y".repeat(200)} end-${i}`)
    await press(t, "l") // logs of the selected service, focused
    const copied: string[] = []
    const real = clipboard.copy
    clipboard.copy = async (_r, text) => (copied.push(text), { ok: true, via: "test" })
    try {
      await press(t, "v") // cursor on the newest line
      expect(t.captureCharFrame()).toContain("copy mode")
      await press(t, "k")
      await press(t, "k") // row-3
      await press(t, "v") // anchor
      await press(t, "j") // row-4
      expect(t.captureCharFrame()).toContain("2 selected")
      await press(t, "y")
      expect(copied).toHaveLength(1)
      const lines = copied[0]!.split("\n")
      expect(lines).toHaveLength(2)
      expect(lines[0]).toStartWith("row-3 ")
      expect(lines[0]).toEndWith("end-3") // not cut, not wrapped
      expect(lines[1]).toEndWith("end-4")
      expect(t.captureCharFrame()).not.toContain("selected")

      await press(t, "Y")
      expect(copied[1]!.split("\n")).toHaveLength(5)
    } finally {
      clipboard.copy = real
    }
  })

  test("logs: E exports the visible lines to the state dir", async () => {
    const t = await setup()
    const svc = t.sup.names[0]!
    t.sup.logs.append(svc, "stdout", "alpha one")
    t.sup.logs.append(svc, "stdout", "beta two")
    await press(t, "l")
    const copied: string[] = []
    const real = clipboard.copy
    clipboard.copy = async (_r, text) => (copied.push(text), { ok: true, via: "test" })
    try {
      await press(t, "E")
      await Bun.sleep(30)
    } finally {
      clipboard.copy = real
    }
    const { readdirSync, readFileSync } = await import("node:fs")
    const dir = `${t.sup.stateDir}/exports`
    const files = readdirSync(dir).filter((f) => f.startsWith(`${svc}-`))
    expect(files.length).toBeGreaterThan(0)
    const body = readFileSync(`${dir}/${files.at(-1)}`, "utf8")
    expect(body).toContain("alpha one")
    expect(body).toContain("beta two")
    expect(copied).toEqual([`${dir}/${files.at(-1)}`])
    expect(t.captureCharFrame()).toContain("path copied")
  })

  test("logs: search highlights without hiding lines, n/N jump between matches", async () => {
    const t = await setup()
    const svc = t.sup.names[0]!
    for (let i = 1; i <= 60; i++) t.sup.logs.append(svc, "stdout", i % 20 === 0 ? `needle-${i}` : `hay-${i}`)
    await press(t, "l")
    await press(t, "/")
    await press(t, "\t") // filter bar → search bar
    for (const ch of "needle") await press(t, ch)
    await press(t, "\r")
    let frame = t.captureCharFrame()
    expect(frame).toContain("hay-") // nothing is hidden
    expect(frame).toContain("match 3/3") // enter lands on the newest
    await press(t, "N")
    expect(t.captureCharFrame()).toContain("match 2/3")
    await press(t, "N")
    await press(t, "N")
    expect(t.captureCharFrame()).toContain("match 3/3") // wraps
    await press(t, "n")
    expect(t.captureCharFrame()).toContain("match 1/3")
    await press(t, "\x1b") // esc clears the search
    await press(t, "n")
    expect(t.captureCharFrame()).toContain("no search")
  })

  test("logs bar: tab keeps the typed text, esc clears filter and search in any mode", async () => {
    const t = await setup()
    const svc = t.sup.names[0]!
    t.sup.logs.append(svc, "stdout", "alpha")
    t.sup.logs.append(svc, "stdout", "beta")
    await press(t, "l")
    await press(t, "/")
    for (const ch of "alp") await press(t, ch)
    expect(t.captureCharFrame()).not.toContain("beta")
    await press(t, "\t")
    let frame = t.captureCharFrame()
    expect(frame).toContain("search alp")
    expect(frame).toContain("beta") // the filter is gone, the text now only highlights
    await press(t, "\r")
    await press(t, "\x1b") // normal mode: esc clears what is kept
    await press(t, "/")
    frame = t.captureCharFrame()
    expect(frame).toContain("highlight matches") // placeholder: the input is empty
    await press(t, "\x1b")
    expect(t.captureCharFrame()).toContain("start/stop") // normal footer is back
  })

  // the fixture lives inside this repo, so only the "not installed" path is reachable
  test.skipIf(!!Bun.which("lazygit"))("L without lazygit shows a toast", async () => {
    const t = await setup()
    await press(t, "L")
    expect(t.captureCharFrame()).toContain("lazygit is not installed")
  })

  test("a project without services renders every view and ignores navigation keys", async () => {
    const sup = new Supervisor(loadConfig({ dir: mkdtempSync(`${tmpdir()}/orbit-empty-`), allowEmpty: true }))
    const t = await testRender(<App sup={sup} onQuit={() => {}} />, { width: 150, height: 40 })
    cleanup = () => t.renderer.destroy()
    noActEnvironment()
    await t.renderOnce()
    expect(t.captureCharFrame()).toContain("This project has no services")
    for (const k of ["j", "k", "g", "G", "2", "3", "1"]) await press(t, k)
    expect(t.captureCharFrame()).toContain("This project has no services")
  })

  test("number keys follow the view registry", async () => {
    const t = await setup()
    await press(t, "3")
    expect(t.captureCharFrame()).toContain("Logs · all services")
    await press(t, "9")
    expect(t.captureCharFrame()).toContain("Logs · all services")
  })

  describe("project picker", () => {
    const calls: Array<[string, string]> = []
    beforeEach(() => {
      process.env.XDG_CONFIG_HOME = mkdtempSync(`${tmpdir()}/orbit-config-`) // each test gets its own registry
    })
    async function setupPicker(opts: { fail?: string; running?: boolean } = {}) {
      calls.length = 0
      // `running`: a project whose only service is a real process that gets started
      const dir = opts.running ? projectDir("current", "services:\n  s:\n    cmd: sleep 30\n") : `${import.meta.dir}/fixtures/stack`
      const sup = new Supervisor(loadConfig({ dir }))
      const onOpenProject = async (dir: string, how: "stop" | "detach") => {
        calls.push([dir, how])
        return opts.fail
      }
      const t = await testRender(<App sup={sup} onQuit={() => {}} onOpenProject={onOpenProject} />, { width: 150, height: 40 })
      cleanup = () => t.renderer.destroy()
      noActEnvironment()
      await t.renderOnce()
      if (opts.running) expect(await sup.start("s")).toBe(true)
      return { ...t, sup }
    }
    const type = async (t: Awaited<ReturnType<typeof setupPicker>>, text: string) => {
      await act(async () => {
        await t.mockInput.typeText(text)
        await Bun.sleep(30)
      })
      await t.renderOnce()
    }
    const projectDir = (name: string, body = "services: {}\n") => {
      const dir = join(mkdtempSync(`${tmpdir()}/orbit-pick-`), name)
      mkdirSync(dir)
      writeFileSync(join(dir, "orbit.yaml"), `name: ${name}\n${body}`)
      return dir
    }

    test("P lists pinned and recent projects, enter opens one right away when nothing is running", async () => {
      const a = projectDir("alpha")
      const b = projectDir("beta")
      registerProject(a, "alpha", 100)
      registerProject(b, "beta", 200)
      setPinned(a, true)
      const t = await setupPicker()
      await press(t, "P")
      const frame = t.captureCharFrame()
      expect(frame).toContain("Projects")
      expect(frame).toContain("★ alpha")
      expect(frame.indexOf("alpha")).toBeLessThan(frame.indexOf("beta"))

      await press(t, "ARROW_DOWN") // j/k are search text here; only the arrows move
      await press(t, "RETURN")
      expect(calls).toEqual([[b, "stop"]])
    })

    test("startWithPicker opens it on launch", async () => {
      registerProject(projectDir("alpha"), "alpha", 100)
      const sup = new Supervisor(loadConfig({ dir: mkdtempSync(`${tmpdir()}/orbit-bare-`), allowEmpty: true }))
      const t = await testRender(<App sup={sup} onQuit={() => {}} startWithPicker />, { width: 150, height: 40 })
      cleanup = () => t.renderer.destroy()
      noActEnvironment()
      await t.renderOnce()
      await Bun.sleep(30)
      await t.renderOnce()
      expect(t.captureCharFrame()).toContain("Projects")
      expect(t.captureCharFrame()).toContain("alpha")
    })

    test("typing filters; a folder path adds an open-folder row; esc closes", async () => {
      const a = projectDir("alpha")
      registerProject(a, "alpha", 100)
      registerProject(projectDir("beta"), "beta", 200)
      const t = await setupPicker()
      await press(t, "P")
      await type(t, "alp")
      let frame = t.captureCharFrame()
      expect(frame).toContain("alpha")
      expect(frame).not.toContain("beta")

      await press(t, "ESCAPE")
      expect(t.captureCharFrame()).not.toContain("Projects")

      await press(t, "P")
      const typed = projectDir("gamma")
      await type(t, typed)
      expect(t.captureCharFrame()).toContain(`Open folder ${typed}`)
      await press(t, "RETURN")
      expect(calls).toEqual([[typed, "stop"]])
    })

    test("tab completes a typed path", async () => {
      const parent = mkdtempSync(`${tmpdir()}/orbit-tab-`)
      mkdirSync(join(parent, "unique-project-dir"))
      const t = await setupPicker()
      await press(t, "P")
      await type(t, `${parent}/uniq`)
      await press(t, "TAB")
      expect(t.captureCharFrame()).toContain(`${parent}/unique-project-dir/`)
      await type(t, "sub") // the cursor must be at the end after completing
      expect(t.captureCharFrame()).toContain(`${parent}/unique-project-dir/sub`)
      await press(t, "BACKSPACE")
      await press(t, "BACKSPACE")
      await press(t, "BACKSPACE")
      await press(t, "RETURN")
      expect(calls).toEqual([[`${parent}/unique-project-dir`, "stop"]])
    })

    test("a path that is not a folder cannot be opened", async () => {
      const t = await setupPicker()
      await press(t, "P")
      await type(t, "/no/such/folder")
      expect(t.captureCharFrame()).toContain("not a folder")
      await press(t, "RETURN")
      expect(calls).toEqual([])
      expect(t.captureCharFrame()).toContain("is not a folder")
    })

    test("with services running it asks: s stops them, d leaves them running, esc cancels", async () => {
      const dir = projectDir("next")
      registerProject(dir, "next", 100)
      const t = await setupPicker({ running: true })
      try {
        await press(t, "P")
        await press(t, "RETURN")
        expect(t.captureCharFrame()).toContain("Switch project")
        expect(calls).toEqual([])

        await press(t, "ESCAPE")
        expect(t.captureCharFrame()).not.toContain("Switch project")
        expect(calls).toEqual([])

        await press(t, "P")
        await press(t, "RETURN")
        await press(t, "d")
        expect(calls).toEqual([[dir, "detach"]])
      } finally {
        await t.sup.dispose()
      }
    })

    test("a failed switch shows the error and goes back to normal; q afterwards still says quit", async () => {
      const dir = projectDir("nope")
      registerProject(dir, "nope", 100)
      const t = await setupPicker({ fail: "nope is already open in another orbit (pid 1)", running: true })
      try {
        await press(t, "P")
        await press(t, "RETURN")
        await press(t, "s")
        await Bun.sleep(30)
        await t.renderOnce()
        const frame = t.captureCharFrame()
        expect(frame).toContain("already open in another orbit")
        expect(frame).not.toContain("Stopping")
        await press(t, "q")
        expect(t.captureCharFrame()).toContain("stop all & quit")
      } finally {
        await t.sup.dispose()
      }
    })

    test("ctrl+f pins and ctrl+x forgets the selected project", async () => {
      registerProject(projectDir("alpha"), "alpha", 100)
      registerProject(projectDir("beta"), "beta", 200)
      const t = await setupPicker()
      await press(t, "P")
      await act(async () => {
        t.mockInput.pressKey("x", { ctrl: true })
        await Bun.sleep(30)
      })
      await t.renderOnce()
      expect(readProjects().map((p) => p.name)).toEqual(["alpha"])
      await act(async () => {
        t.mockInput.pressKey("f", { ctrl: true })
        await Bun.sleep(30)
      })
      expect(readProjects()[0]!.pinned).toBe(true)
    })
  })

  test("T opens the theme picker: arrows preview, esc reverts, enter saves", async () => {
    const t = await setup()
    const until = async (cond: () => boolean) => {
      for (let i = 0; i < 40 && !cond(); i++) {
        await Bun.sleep(25)
        await t.renderOnce()
      }
    }
    await press(t, "T")
    await until(() => t.captureCharFrame().includes("catppuccin-mocha"))
    expect(t.captureCharFrame()).toContain("catppuccin-mocha")
    await press(t, "j")
    await until(() => theme.bg === THEMES["catppuccin-mocha"]!.bg)
    expect(theme.bg).toBe(THEMES["catppuccin-mocha"]!.bg)
    await press(t, "ESCAPE")
    await until(() => theme.bg === THEMES.orbit!.bg)
    expect(theme.bg).toBe(THEMES.orbit!.bg)
    expect(readUserConfig()).toEqual({})

    await press(t, "T")
    await press(t, "j")
    await press(t, "j")
    await press(t, "RETURN")
    await until(() => theme.bg === THEMES["catppuccin-macchiato"]!.bg)
    expect(theme.bg).toBe(THEMES["catppuccin-macchiato"]!.bg)
    expect(readUserConfig()).toEqual({ theme: "catppuccin-macchiato" })
    expect(t.captureCharFrame()).toContain("theme: catppuccin-macchiato")
  })
  test("i opens an interactive console, ctrl+] hides it and i brings it back", async () => {
    const dir = mkdtempSync(join(tmpdir(), "orbit-console-"))
    writeFileSync(join(dir, "orbit.yaml"), "name: c\nservices:\n  box:\n    cmd: sleep 60\n    console: cat\n    autostart: false\n")
    const sup = new Supervisor(loadConfig({ dir }))
    const t = await testRender(<App sup={sup} onQuit={() => {}} />, { width: 120, height: 36 })
    cleanup = () => t.renderer.destroy()
    noActEnvironment()
    await t.renderOnce()
    const frame = () => t.captureCharFrame()
    const settle = async () => {
      await Bun.sleep(150)
      await t.renderOnce()
    }

    await press(t, "i")
    await settle()
    expect(frame()).toContain("› box · cat")
    await t.mockInput.typeText("hello")
    await settle()
    expect(frame()).toContain("hello")

    await act(async () => t.mockInput.pressKey("]", { ctrl: true }))
    await settle()
    expect(frame()).not.toContain("› box")

    await press(t, "i")
    await settle()
    expect(frame()).toContain("› box · cat")
    expect(frame()).toContain("hello")

    // ctrl+d ends cat (the first one only flushes the pending "hello"): the modal closes on its own
    await act(async () => t.mockInput.pressKey("d", { ctrl: true }))
    await act(async () => t.mockInput.pressKey("d", { ctrl: true }))
    await Bun.sleep(300)
    await t.renderOnce()
    expect(frame()).not.toContain("› box")
  })
})
