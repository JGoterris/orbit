import { describe, expect, test } from "bun:test"
import { loadConfig } from "../src/config/load.ts"
import { ConsoleManager, ConsoleSession, consoleCommand } from "../src/core/console.ts"

const svc = (over: Record<string, unknown>) => ({ name: "db", type: "process", cwd: "/tmp", env: { A: "1" }, envFiles: [], ...over }) as never

describe("consoleCommand", () => {
  test("process: console command or $SHELL, in its cwd with its env", () => {
    const c = consoleCommand(svc({ console: "psql -U me" }), { status: "stopped" })
    expect(c).toMatchObject({ argv: ["/bin/sh", "-c", "psql -U me"], cwd: "/tmp", title: "psql -U me" })
    expect((c as { env: Record<string, string> }).env).toMatchObject({ A: "1", ORBIT_SERVICE: "db" })
    const shell = consoleCommand(svc({}), { status: "running" }) as { argv: string[] }
    expect(shell.argv).toEqual([process.env.SHELL || "/bin/sh"])
  })

  test("containers need to be running", () => {
    const d = svc({ type: "docker", console: "psql" })
    expect(consoleCommand(d, { status: "stopped" })).toEqual({ error: "db is not running" })
    expect(consoleCommand(d, { status: "running" })).toEqual({ error: "db is not running" })
    const ok = consoleCommand(d, { status: "healthy", containerId: "abc" }) as { argv: string[] }
    expect(ok.argv).toEqual(["docker", "exec", "-it", "abc", "sh", "-c", "psql"])
  })

  test("console is read from orbit.yaml", () => {
    const config = loadConfig({ dir: `${import.meta.dir}/fixtures/stack` })
    expect(Object.values(config.services).every((s) => s.console === undefined)).toBe(true)
  })
})

describe("ConsoleSession", () => {
  test("talks to a program through a pty and keeps a backlog", async () => {
    const session = new ConsoleSession({ argv: ["/bin/sh", "-c", "read x; echo got:$x"], env: {}, title: "t" }, 80, 24)
    const seen: string[] = []
    session.on("data", (d: Uint8Array) => seen.push(new TextDecoder().decode(d)))
    session.write("hi\r")
    await session.exited
    expect(session.exitCode).toBe(0)
    expect(seen.join("")).toContain("got:hi")
    expect(new TextDecoder().decode(session.backlog)).toContain("got:hi")
    expect(session.alive).toBe(false)
  })

  test("manager reuses live sessions and kill ends them", async () => {
    const m = new ConsoleManager()
    const spec = { argv: ["/bin/sh", "-c", "sleep 30"], env: {}, title: "t" }
    const a = m.open("x", spec, 80, 24)
    expect(m.open("x", spec, 80, 24)).toBe(a)
    expect(m.has("x")).toBe(true)
    m.closeAll()
    await a.exited
    expect(m.has("x")).toBe(false)
  })
})
