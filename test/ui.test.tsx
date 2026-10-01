import { afterEach, describe, expect, test } from "bun:test"
import { testRender } from "@opentui/react/test-utils"
import { loadConfig } from "../src/config/load.ts"
import { Supervisor } from "../src/core/supervisor.ts"
import { App } from "../src/ui/App.tsx"

// The app has live timers (spinners, clocks), so rendering is driven with renderOnce() + short
// waits instead of act(); testRender() turns the act environment on, so switch it off again.
const noActEnvironment = () => ((globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = false)
const act = async (fn: () => Promise<void>) => fn()

let cleanup: (() => void) | undefined
afterEach(() => cleanup?.())

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

  // the fixture lives inside this repo, so only the "not installed" path is reachable
  test.skipIf(!!Bun.which("lazygit"))("L without lazygit shows a toast", async () => {
    const t = await setup()
    await press(t, "L")
    expect(t.captureCharFrame()).toContain("lazygit is not installed")
  })
})
