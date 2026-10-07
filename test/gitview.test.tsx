import { afterEach, describe, expect, test } from "bun:test"
import { testRender } from "@opentui/react/test-utils"
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadConfig } from "../src/config/load.ts"
import { GitRepo } from "../src/core/git/repo.ts"
import { readStatus } from "../src/core/git/status.ts"
import { Supervisor } from "../src/core/supervisor.ts"
import { App } from "../src/ui/App.tsx"
import { clipboard } from "../src/ui/clipboard.ts"
import { applyTheme } from "../src/ui/theme.ts"
import { THEMES } from "../src/ui/themes.ts"

process.env.XDG_STATE_HOME = mkdtempSync(`${tmpdir()}/orbit-state-`)
process.env.XDG_CONFIG_HOME = mkdtempSync(`${tmpdir()}/orbit-config-`)
const noActEnvironment = () => ((globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = false)

let cleanup: (() => void) | undefined
afterEach(() => {
  cleanup?.()
  applyTheme(THEMES.orbit!)
})

const git = (cwd: string, ...args: string[]) => {
  const r = Bun.spawnSync(["git", ...args], { cwd })
  if (r.exitCode !== 0) throw new Error(`git ${args.join(" ")}: ${r.stderr}`)
  return r.stdout.toString()
}

/** A project that is a git repo with a.txt (30 lines) and b.txt committed. */
function project() {
  const dir = mkdtempSync(join(tmpdir(), "orbit-gview-"))
  git(dir, "init", "-q", "-b", "main")
  git(dir, "config", "user.name", "Test")
  git(dir, "config", "user.email", "t@example.com")
  writeFileSync(join(dir, "orbit.yaml"), "name: gv\nservices:\n  s:\n    cmd: sleep 30\n    autostart: false\n")
  writeFileSync(join(dir, "a.txt"), Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join("\n") + "\n")
  writeFileSync(join(dir, "b.txt"), "b\n")
  git(dir, "add", ".")
  git(dir, "commit", "-q", "-m", "first commit")
  return dir
}

async function setup(dir = project(), opts: { git?: GitRepo | null } = {}) {
  const quits: string[] = []
  const sup = new Supervisor(loadConfig({ dir, allowEmpty: true }))
  const t = await testRender(<App sup={sup} onQuit={(how) => void quits.push(how)} git={opts.git === undefined ? new GitRepo(dir) : opts.git} />, { width: 160, height: 44 })
  cleanup = () => t.renderer.destroy()
  noActEnvironment()
  await t.renderOnce()
  return { ...t, dir, quits }
}
type T = Awaited<ReturnType<typeof setup>>

async function press(t: T, key: string, wait = 40) {
  t.mockInput.pressKey(key)
  await Bun.sleep(wait)
  await t.renderOnce()
}
async function type(t: T, text: string) {
  await t.mockInput.typeText(text)
  await Bun.sleep(40)
  await t.renderOnce()
}
/** waits for async git work (and the repaint that follows) */
async function settle(t: T, ms = 400) {
  await Bun.sleep(ms)
  await t.renderOnce()
}
const frame = (t: T) => t.captureCharFrame()
async function openGit(t: T) {
  await press(t, "4")
  await settle(t)
}
const toChanges = (t: T) => press(t, "TAB") // the view opens on Repos; Changes is next

describe("git view", () => {
  test("4 opens it: the repo table, changes, graph, and the diff of the selected file", async () => {
    const dir = project()
    writeFileSync(join(dir, "a.txt"), readFileSync(join(dir, "a.txt"), "utf8").replace("line 2\n", "TWO\n"))
    writeFileSync(join(dir, "new.txt"), "n\n")
    const t = await setup(dir)
    await settle(t)
    expect(frame(t)).not.toContain("⎇") // the header only has the services' counters
    expect(frame(t)).not.toContain("✎2")

    await openGit(t)
    const f = frame(t)
    expect(f).toContain("Repos · 1 · main ×1")
    expect(f).toMatch(/● \S+\s+main\s+no upstream\s+✎2/)
    expect(f).toContain("Changes · main")
    expect(f).toContain("a.txt")
    expect(f).toContain("new.txt")
    expect(f).toContain("Graph")
    expect(f).toContain("first commit") // in the table (last commit) and in the graph
    expect(f).toContain("Diff · a.txt · unstaged")
    expect(f).toContain("+TWO")
    expect(f).toContain("-line 2")
  })

  test("not a repository", async () => {
    const dir = mkdtempSync(join(tmpdir(), "orbit-norepo-"))
    const t = await setup(dir, { git: null })
    await press(t, "4")
    expect(frame(t)).toContain("None of this project's services are in a git repository")
    expect(frame(t)).not.toContain("⎇")
  })

  test("enter on the Repos table works like tab: focus goes to Changes, nothing else opens", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    expect(frame(t)).toContain("pick repo") // Repos is focused
    await press(t, "RETURN")
    expect(frame(t)).toContain("stage") // Changes' hints
    expect(frame(t)).not.toContain("pick repo")
    expect(frame(t)).toContain("Changes ·") // orbit is still on screen (no lazygit took the terminal)
  })

  test("space stages and unstages the selected file", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    await press(t, " ")
    await settle(t)
    expect((await readStatus(dir))!.files[0]).toMatchObject({ path: "b.txt", x: "M", y: "." })
    expect(frame(t)).toContain("staged b.txt")
    expect(frame(t)).toContain("1 staged")
    await press(t, " ")
    await settle(t)
    expect((await readStatus(dir))!.files[0]).toMatchObject({ x: ".", y: "M" })
  })

  test("c commits with a message typed into the prompt; letters like q do not reach the shell", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    await press(t, "c")
    expect(frame(t)).toContain("nothing is staged")
    expect(frame(t)).not.toContain("enter commit")

    await press(t, "a") // stage all
    await settle(t)
    await press(t, "c")
    expect(frame(t)).toContain("enter commit")
    await type(t, "quick fix: quit early")
    expect(frame(t)).toContain("quick fix: quit early")
    await press(t, "RETURN")
    await settle(t)
    expect(t.quits).toEqual([]) // the q in "quick" and "quit" went to the input
    expect(git(dir, "log", "-1", "--format=%s").trim()).toBe("quick fix: quit early")
    expect(frame(t)).toContain("committed: quick fix: quit early")
    expect(frame(t)).toContain("working tree clean")
  })

  test("esc cancels the prompt without committing", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    await press(t, "a")
    await settle(t)
    await press(t, "c")
    await type(t, "never")
    await press(t, "ESCAPE")
    expect(frame(t)).not.toContain("enter commit")
    expect(git(dir, "log", "--format=%s").trim()).toBe("first commit")
  })

  test("what moved to lazygit is gone: d does not discard, s does not stash", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    await press(t, "d")
    await press(t, "s")
    expect(frame(t)).not.toContain("Throw away")
    expect(frame(t)).not.toContain("Stash changes")
    expect(readFileSync(join(dir, "b.txt"), "utf8")).toBe("changed\n")
  })

  test("branches: n creates and switches, enter switches back", async () => {
    const dir = project()
    const t = await setup(dir)
    await openGit(t)
    await press(t, "TAB")
    await press(t, "TAB") // branches
    await press(t, "n")
    await type(t, "feature-x")
    await press(t, "RETURN")
    await settle(t)
    expect(git(dir, "branch", "--show-current").trim()).toBe("feature-x")
    expect(frame(t)).toContain("on feature-x")
    expect(frame(t)).toMatch(/Repos · 1 · feature-x ×1/)

    await press(t, "j")
    await press(t, "RETURN")
    await settle(t)
    expect(git(dir, "branch", "--show-current").trim()).toBe("main")
  })

  test("leaving the view hands the keyboard back to the service shortcuts", async () => {
    const dir = project()
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    await press(t, "1")
    // `c` is "clear logs" in the service views; the git view's `c` (commit) must be gone
    await press(t, "c")
    expect(frame(t)).toContain("logs cleared")
    expect(frame(t)).not.toContain("nothing is staged")
  })

  test("a failing git operation shows git's own message", async () => {
    const dir = project()
    const t = await setup(dir)
    await openGit(t)
    await press(t, "p") // no upstream to pull from
    await settle(t, 600)
    expect(frame(t)).toContain("pulled 0/1")
    expect(frame(t)).toMatch(/✗ .*(tracking|upstream|remote)/i) // also in the repo's row
  })

  test("s toggles a side-by-side diff and back", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    await press(t, "RETURN")
    await press(t, "s")
    await settle(t, 300)
    let f = frame(t)
    expect(f).toContain("split")
    expect(f).toContain("changed")
    await press(t, "s")
    f = frame(t)
    expect(f).not.toContain("· split")
    expect(f).toContain("+changed")
  })

  test("[ ] move between hunks and { } between files, read only", async () => {
    const dir = project()
    writeFileSync(join(dir, "a.txt"), readFileSync(join(dir, "a.txt"), "utf8").replace("line 2\n", "TWO\n").replace("line 29\n", "TWENTY-NINE\n"))
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    await press(t, "RETURN")
    expect(frame(t)).toContain("hunk 1/2")
    await press(t, "]")
    expect(frame(t)).toContain("hunk 2/2")
    await press(t, " ") // there is no staging from the diff any more
    await settle(t)
    expect(git(dir, "diff", "--cached")).toBe("")
    await press(t, "}")
    expect(frame(t)).toContain("Diff · b.txt")
  })

  test("the graph shows the diff of the selected commit", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "second\n")
    git(dir, "commit", "-qam", "touch b")
    const t = await setup(dir)
    await openGit(t)
    for (let i = 0; i < 3; i++) await press(t, "TAB") // changes, branches, graph
    await settle(t, 300)
    expect(frame(t)).toContain("touch b")
    expect(frame(t)).toContain("+second")
    await press(t, "j")
    await settle(t, 300)
    await t.renderOnce()
    expect(frame(t)).toContain("+line 1") // the first commit added a.txt
    expect(frame(t)).toContain("a.txt (new)")
  })

  test("the diff follows the selected branch", async () => {
    const dir = project()
    git(dir, "switch", "-q", "-c", "other")
    writeFileSync(join(dir, "o.txt"), "o\n")
    git(dir, "add", ".")
    // branches are listed newest first and ties break by name: an old date keeps `main` first, `other` second on any machine
    const old = { ...process.env, GIT_COMMITTER_DATE: "2000-01-01T00:00:00Z", GIT_AUTHOR_DATE: "2000-01-01T00:00:00Z" }
    expect(Bun.spawnSync(["git", "commit", "-q", "-m", "on other"], { cwd: dir, env: old }).exitCode).toBe(0)
    git(dir, "switch", "-q", "main")
    const t = await setup(dir)
    await openGit(t)
    await press(t, "TAB")
    await press(t, "TAB") // branches
    await settle(t, 300)
    expect(frame(t)).toContain("other")
    await press(t, "j")
    await settle(t, 300)
    expect(frame(t)).toContain("HEAD...other")
    expect(frame(t)).toContain("+o")
  })

  test("Changes is a tree: folders fold with enter, space on a folder stages all of it, { } skip folders", async () => {
    const dir = project()
    mkdirSync(join(dir, "src", "ui"), { recursive: true })
    writeFileSync(join(dir, "src", "ui", "one.ts"), "1\n")
    writeFileSync(join(dir, "src", "ui", "two.ts"), "2\n")
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    let f = frame(t)
    expect(f).toMatch(/▾ src\/ui\/\s+2/) // single-child folders merged, with their change count
    expect(f).toMatch(/\?\s+one\.ts/)
    expect(f).toContain("b.txt")
    expect(f).not.toContain("src/ui/one.ts") // shown by name under the folder, not as a path

    await press(t, "RETURN") // on the folder: fold it
    await settle(t, 200)
    f = frame(t)
    expect(f).toMatch(/▸ src\/ui\/\s+2/)
    expect(f).not.toContain("one.ts")
    await press(t, "RETURN") // unfold
    await settle(t, 200)
    expect(frame(t)).toContain("one.ts")

    await press(t, " ") // stage the whole folder
    await settle(t)
    const st = (await readStatus(dir))!.files
    expect(st.filter((c) => c.path.startsWith("src/ui/")).every((c) => c.x === "A")).toBe(true)
    expect(st.find((c) => c.path === "b.txt")).toMatchObject({ x: ".", y: "M" })
    expect(frame(t)).toContain("staged src/ui/")
    await press(t, " ") // all staged: unstage again
    await settle(t)
    expect((await readStatus(dir))!.files.find((c) => c.path === "src/ui/one.ts")!.kind).toBe("untracked")

    // the folder row has no diff; { } from a file's diff jump over it
    await press(t, "j") // one.ts
    await press(t, "RETURN")
    expect(frame(t)).toContain("Diff · src/ui/one.ts")
    await press(t, "}")
    expect(frame(t)).toContain("Diff · src/ui/two.ts")
    await press(t, "}")
    expect(frame(t)).toContain("Diff · b.txt")
  })

  test("layout: Graph under Branches on the left, the big Diff over Commands on the right", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    const lines = frame(t).split("\n")
    const row = (re: RegExp) => lines.findIndex((l) => re.test(l))
    const col = (re: RegExp) => lines[row(re)]!.search(re)
    const [branches, graph, stash] = [row(/╭─ Branches/), row(/╭─ Graph/), row(/╭─ Stash/)]
    expect(branches).toBeGreaterThan(0)
    expect(branches).toBeLessThan(graph)
    expect(graph).toBeLessThan(stash)
    expect(col(/╭─ Graph/)).toBe(col(/╭─ Branches/)) // same column
    const [diff, cmds] = [row(/╭─ Diff/), row(/╭─ Commands/)]
    expect(col(/╭─ Diff/)).toBeGreaterThan(col(/╭─ Graph/)) // right of the left column
    expect(col(/╭─ Commands/)).toBe(col(/╭─ Diff/))
    expect(diff).toBeLessThan(cmds)
    expect(cmds - diff).toBeGreaterThan(10) // the diff is the biggest panel on the right
  })

  test("Commands lists the git commands orbit ran, with their outcome", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    expect(frame(t)).toContain("no git command run yet")
    await toChanges(t)
    await press(t, " ") // stage b.txt
    await settle(t)
    await press(t, "c")
    await type(t, "log me")
    await press(t, "RETURN")
    await settle(t)
    await press(t, "p") // fails: no upstream
    await settle(t, 600)
    const f = frame(t)
    expect(f).toContain("git add -- b.txt")
    expect(f).toContain('git commit -F -')
    expect(f).toContain("git pull --ff-only")
    expect(f).toMatch(/✗ \S+\s+git pull --ff-only/)
    expect(f).toMatch(/✓ \S+\s+git add -- b\.txt/)
    // polling reads (status, log…) are not listed
    expect(f).not.toContain("git status")
    expect(f).not.toContain("git log")
  })

  test("zoom shows only the focused panel", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await toChanges(t)
    await press(t, "z")
    expect(frame(t)).toContain("Changes")
    expect(frame(t)).not.toContain("Branches")
    expect(frame(t)).not.toContain("Repos ·")
    expect(frame(t)).not.toContain("Diff ·")
    await press(t, "ESCAPE")
    expect(frame(t)).toContain("Branches")
    expect(frame(t)).toContain("Repos ·")
  })
})

/** A workspace (not a repo) whose services api and web live in two separate repos. */
function workspace() {
  const ws = mkdtempSync(join(tmpdir(), "orbit-gws-"))
  for (const name of ["api", "web"]) {
    const dir = join(ws, name)
    mkdirSync(dir)
    git(dir, "init", "-q", "-b", name === "api" ? "main" : "develop")
    git(dir, "config", "user.name", "Test")
    git(dir, "config", "user.email", "t@example.com")
    writeFileSync(join(dir, "base.txt"), "base\n")
    git(dir, "add", ".")
    git(dir, "commit", "-q", "-m", `${name} first`)
  }
  writeFileSync(
    join(ws, "orbit.yaml"),
    ["name: ws", "services:", "  api: { cmd: sleep 30, cwd: ./api, autostart: false }", "  web: { cmd: sleep 30, cwd: ./web, autostart: false }", ""].join("\n"),
  )
  return ws
}

async function setupWorkspace(ws: string) {
  const quits: string[] = []
  const sup = new Supervisor(loadConfig({ dir: ws }))
  const t = await testRender(<App sup={sup} onQuit={(how) => void quits.push(how)} />, { width: 160, height: 44 })
  cleanup = () => t.renderer.destroy()
  noActEnvironment()
  await t.renderOnce()
  return { ...t, dir: ws, quits }
}

const branchOf = (ws: string, name: string) => git(join(ws, name), "branch", "--show-current").trim()

describe("git view with services in different repos", () => {
  test("the table shows every repo: branch, changes, last commit and services at a glance", async () => {
    const ws = workspace()
    writeFileSync(join(ws, "api", "a1.txt"), "1\n")
    writeFileSync(join(ws, "api", "a2.txt"), "2\n")
    writeFileSync(join(ws, "web", "w1.txt"), "1\n")
    const t = await setupWorkspace(ws)
    await settle(t, 600)
    expect(frame(t)).not.toContain("⎇ 2 repos")

    await openGit(t)
    const f = frame(t)
    expect(f).toContain("Repos · 2 · develop ×1 · main ×1")
    expect(f).toMatch(/● api\s+main\s.*✎2\s+\S+ api first/)
    expect(f).toMatch(/● web\s+develop\s.*✎1\s+\S+ web first/)
  })

  test("picking another repo switches every panel to it, and single-repo actions only touch that repo", async () => {
    const ws = workspace()
    writeFileSync(join(ws, "api", "a1.txt"), "1\n")
    writeFileSync(join(ws, "web", "w1.txt"), "1\n")
    const t = await setupWorkspace(ws)
    await openGit(t)
    expect(frame(t)).toContain("pick repo") // opens on the Repos table
    expect(frame(t)).toContain("Diff · a1.txt")
    await press(t, "j")
    await settle(t, 300)
    expect(frame(t)).toContain("Diff · w1.txt")
    expect(frame(t)).toContain("Changes · develop")

    await press(t, "TAB") // changes of web
    await press(t, " ")
    await settle(t)
    expect(git(join(ws, "web"), "diff", "--cached", "--name-only").trim()).toBe("w1.txt")
    expect(git(join(ws, "api"), "diff", "--cached", "--name-only").trim()).toBe("")
  })

  test("opens on the repo of the service that was selected", async () => {
    const ws = workspace()
    writeFileSync(join(ws, "api", "a1.txt"), "1\n")
    writeFileSync(join(ws, "web", "w1.txt"), "1\n")
    const t = await setupWorkspace(ws)
    await press(t, "j") // select the second service: web
    await press(t, "4")
    await settle(t, 300)
    expect(frame(t)).toContain("Diff · w1.txt")
    expect(frame(t)).toContain("Changes · develop")
  })

  test("space marks repos; the marks show in the table and a to mark all / none", async () => {
    const ws = workspace()
    const t = await setupWorkspace(ws)
    await openGit(t)
    expect(frame(t)).toContain("none marked: all")
    await press(t, " ")
    expect(frame(t)).toMatch(/✓ ● api/)
    expect(frame(t)).toContain("1 marked")
    await press(t, "a")
    expect(frame(t)).toContain("2 marked")
    await press(t, "a")
    expect(frame(t)).toContain("none marked: all")
  })

  test("m f fetches all of them", async () => {
    const ws = workspace()
    const t = await setupWorkspace(ws)
    await openGit(t)
    await press(t, "m")
    expect(frame(t)).toContain("new branch") // the footer offers the multi-repo keys
    await press(t, "f")
    await settle(t, 600)
    expect(frame(t)).toContain("fetched 2 repos")
  })

  test("m b: a new branch in every repo, asking first; marked repos only when there are marks", async () => {
    const ws = workspace()
    const t = await setupWorkspace(ws)
    await openGit(t)
    await press(t, "m")
    await press(t, "b")
    await type(t, "feat/x")
    await press(t, "RETURN")
    expect(frame(t)).toContain("New branch feat/x in 2 repos")
    expect(frame(t)).toContain("api, web")
    await press(t, "n") // declined: nothing happens
    expect([branchOf(ws, "api"), branchOf(ws, "web")]).toEqual(["main", "develop"])

    await press(t, "m")
    await press(t, "b")
    await type(t, "feat/x")
    await press(t, "RETURN")
    await press(t, "y")
    await settle(t, 600)
    expect([branchOf(ws, "api"), branchOf(ws, "web")]).toEqual(["feat/x", "feat/x"])
    expect(frame(t)).toContain("new branch 2 repos")
    expect(frame(t)).toContain("Repos · 2 · feat/x ×2")

    // only the marked one
    await press(t, "j") // web
    await press(t, " ")
    await press(t, "m")
    await press(t, "b")
    await type(t, "only-web")
    await press(t, "RETURN")
    expect(frame(t)).toContain("New branch only-web in 1 repo")
    await press(t, "y")
    await settle(t, 600)
    expect([branchOf(ws, "api"), branchOf(ws, "web")]).toEqual(["feat/x", "only-web"])
  })

  test("m s: a repo that cannot switch fails on its own row, the rest still switch", async () => {
    const ws = workspace()
    const t = await setupWorkspace(ws)
    await openGit(t)
    await press(t, "m")
    await press(t, "s")
    await type(t, "develop") // web is on it, api has no such branch
    await press(t, "RETURN")
    await press(t, "y")
    await settle(t, 600)
    expect(frame(t)).toContain("switched 1/2 · api")
    expect(frame(t)).toMatch(/● api .*✗ /)
    expect(frame(t)).toMatch(/● web .*✓ on develop/)
    expect(branchOf(ws, "web")).toBe("develop")
  })

  test("m p pulls every repo after asking; one without upstream fails on its row", async () => {
    const ws = workspace()
    const bare = join(ws, "origin.git")
    git(ws, "init", "-q", "--bare", "-b", "main", bare)
    git(join(ws, "api"), "remote", "add", "origin", bare)
    git(join(ws, "api"), "push", "-q", "-u", "origin", "main")
    const other = join(ws, "other")
    git(ws, "clone", "-q", bare, other)
    git(other, "config", "user.name", "Test")
    git(other, "config", "user.email", "t@example.com")
    writeFileSync(join(other, "new.txt"), "n\n")
    git(other, "add", ".")
    git(other, "commit", "-q", "-m", "from elsewhere")
    git(other, "push", "-q")

    const t = await setupWorkspace(ws)
    await openGit(t)
    await press(t, "m")
    await press(t, "p")
    expect(frame(t)).toContain("Pull 2 repos")
    await press(t, "y")
    await settle(t, 800)
    expect(git(join(ws, "api"), "log", "-1", "--format=%s").trim()).toBe("from elsewhere")
    expect(frame(t)).toContain("pulled 1/2 · web")
    expect(frame(t)).toMatch(/● api .*✓ pulled/)
    expect(frame(t)).toMatch(/● web .*✗ /)
  })

  test("a single repo still gets the table, and shift+tab from Repos lands on the Commands panel", async () => {
    const dir = project()
    const t = await setup(dir)
    await openGit(t)
    expect(frame(t)).toContain("Repos · 1")
    t.mockInput.pressKey("TAB", { shift: true })
    await Bun.sleep(40)
    await t.renderOnce()
    expect(frame(t)).toContain("copy command") // the Commands panel's hints
  })

  test("services outside any repo: the view says so", async () => {
    const ws = mkdtempSync(join(tmpdir(), "orbit-norepos-"))
    writeFileSync(join(ws, "orbit.yaml"), "name: n\nservices:\n  s: { cmd: sleep 30, autostart: false }\n")
    const t = await setupWorkspace(ws)
    await press(t, "4")
    expect(frame(t)).toContain("None of this project's services are in a git repository")
  })
})
