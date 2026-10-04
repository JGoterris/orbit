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

describe("git view", () => {
  test("4 opens it: branch in the header, changes, commits, and the diff of the selected file", async () => {
    const dir = project()
    writeFileSync(join(dir, "a.txt"), readFileSync(join(dir, "a.txt"), "utf8").replace("line 2\n", "TWO\n"))
    writeFileSync(join(dir, "new.txt"), "n\n")
    const t = await setup(dir)
    await settle(t)
    expect(frame(t)).toContain("⎇ main") // header, before even opening the view
    expect(frame(t)).toContain("✎2")

    await openGit(t)
    const f = frame(t)
    expect(f).toContain("Changes · main")
    expect(f).toContain("a.txt")
    expect(f).toContain("new.txt")
    expect(f).toContain("first commit")
    expect(f).toContain("Diff · a.txt · unstaged")
    expect(f).toContain("+TWO")
    expect(f).toContain("-line 2")
    expect(f).toContain("hunk 1/1")
  })

  test("not a repository", async () => {
    const dir = mkdtempSync(join(tmpdir(), "orbit-norepo-"))
    const t = await setup(dir, { git: null })
    await press(t, "4")
    expect(frame(t)).toContain("None of this project's services are in a git repository")
    expect(frame(t)).not.toContain("⎇")
  })

  test("space stages and unstages the selected file", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
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
    await press(t, "c")
    expect(frame(t)).toContain("nothing is staged")
    expect(frame(t)).not.toContain("Commit ")

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
    await press(t, "a")
    await settle(t)
    await press(t, "c")
    await type(t, "never")
    await press(t, "ESCAPE")
    expect(frame(t)).not.toContain("enter commit")
    expect(git(dir, "log", "--format=%s").trim()).toBe("first commit")
  })

  test("stage one hunk from the diff: enter focuses it, ] moves, space stages", async () => {
    const dir = project()
    writeFileSync(join(dir, "a.txt"), readFileSync(join(dir, "a.txt"), "utf8").replace("line 2\n", "TWO\n").replace("line 29\n", "TWENTY-NINE\n"))
    const t = await setup(dir)
    await openGit(t)
    expect(frame(t)).toContain("hunk 1/2")
    await press(t, "RETURN") // focus the diff
    await press(t, "]")
    expect(frame(t)).toContain("hunk 2/2")
    await press(t, " ")
    await settle(t)
    const staged = git(dir, "diff", "--cached")
    expect(staged).toContain("+TWENTY-NINE")
    expect(staged).not.toContain("+TWO")
    expect(git(dir, "diff")).toContain("+TWO")
    expect(frame(t)).toContain("staged hunk")

    // v flips to the staged side of the same file, where space unstages
    await press(t, "v")
    await settle(t)
    expect(frame(t)).toContain("a.txt · staged")
    await press(t, " ")
    await settle(t)
    expect(git(dir, "diff", "--cached")).toBe("")
  })

  test("discarding asks first: n keeps the file, y removes the change", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await press(t, "d")
    expect(frame(t)).toContain("Throw away the changes in b.txt")
    await press(t, "n")
    expect(readFileSync(join(dir, "b.txt"), "utf8")).toBe("changed\n")
    await press(t, "d")
    await press(t, "y")
    await settle(t)
    expect(readFileSync(join(dir, "b.txt"), "utf8")).toBe("b\n")
  })

  test("branches: n creates and switches, enter switches back, the current one cannot be deleted", async () => {
    const dir = project()
    const t = await setup(dir)
    await openGit(t)
    await press(t, "TAB") // branches
    await press(t, "n")
    await type(t, "feature-x")
    await press(t, "RETURN")
    await settle(t)
    expect(git(dir, "branch", "--show-current").trim()).toBe("feature-x")
    expect(frame(t)).toContain("on new branch feature-x")

    await press(t, "d")
    expect(frame(t)).toContain("cannot delete the branch you are on")
    await press(t, "j")
    await press(t, "RETURN")
    await settle(t)
    expect(git(dir, "branch", "--show-current").trim()).toBe(frame(t).includes("on main") ? "main" : "feature-x")
  })

  test("stash: s stashes, o pops", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await press(t, "s")
    await type(t, "wip")
    await press(t, "RETURN")
    await settle(t)
    expect(readFileSync(join(dir, "b.txt"), "utf8")).toBe("b\n")
    expect(frame(t)).toContain("wip")
    await press(t, "TAB")
    await press(t, "TAB")
    await press(t, "TAB") // stash pane
    await press(t, "o")
    await settle(t)
    expect(readFileSync(join(dir, "b.txt"), "utf8")).toBe("changed\n")
  })

  test("leaving the view hands the keyboard back to the service shortcuts", async () => {
    const dir = project()
    const t = await setup(dir)
    await openGit(t)
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
    expect(frame(t)).toContain("pull failed")
  })

  test("s toggles a side-by-side diff and back", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
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

  test("the commits panel shows the diff of the selected commit", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "second\n")
    git(dir, "commit", "-qam", "touch b")
    const t = await setup(dir)
    await openGit(t)
    await press(t, "TAB") // branches
    await press(t, "TAB") // commits
    await settle(t, 300)
    expect(frame(t)).toContain("touch b")
    expect(frame(t)).toContain("+second")
    await press(t, "j")
    await settle(t, 300)
    expect(frame(t)).toContain("+line 1") // the first commit added a.txt
    expect(frame(t)).toContain("a.txt (new)")
  })

  /** the repo with a second commit that modifies a.txt, adds added.txt and deletes b.txt */
  function mixedCommit() {
    const dir = project()
    writeFileSync(join(dir, "a.txt"), readFileSync(join(dir, "a.txt"), "utf8").replace("line 2\n", "TWO\n"))
    writeFileSync(join(dir, "added.txt"), "fresh\n")
    git(dir, "rm", "-q", "b.txt")
    git(dir, "add", "-A")
    git(dir, "commit", "-q", "-m", "mixed changes")
    return dir
  }
  const toCommits = async (t: T) => {
    await press(t, "TAB") // branches
    await press(t, "TAB") // commits
    await settle(t, 300)
  }

  test("enter on a commit lists the files it changed and the diff follows the selected file", async () => {
    const dir = mixedCommit()
    const t = await setup(dir)
    await openGit(t)
    await toCommits(t)
    expect(frame(t)).toContain("mixed changes")

    await press(t, "RETURN")
    await settle(t, 300)
    let f = frame(t)
    const hash = git(dir, "rev-parse", "--short", "HEAD").trim()
    expect(f).toContain(`Files · ${hash}`)
    expect(f).toMatch(/M a\.txt/)
    expect(f).toMatch(/A added\.txt/)
    expect(f).toMatch(/D b\.txt/)
    expect(f).not.toContain("mixed changes") // the commit list is replaced while browsing
    expect(f).toContain(`${hash} · a.txt`)
    expect(f).toContain("+TWO")
    expect(f).not.toContain("+fresh") // only the selected file

    await press(t, "j")
    await settle(t, 300)
    f = frame(t)
    expect(f).toContain(`${hash} · added.txt`)
    expect(f).toContain("+fresh")
    expect(f).not.toContain("+TWO")

    await press(t, "j")
    await settle(t, 300)
    expect(frame(t)).toContain("-b")
    expect(frame(t)).toContain("b.txt (deleted)")

    await press(t, "G") // already last: stays
    await press(t, "g")
    await settle(t, 300)
    expect(frame(t)).toContain("+TWO")
  })

  test("esc steps back one level at a time: diff -> file list -> commit list", async () => {
    const dir = mixedCommit()
    const t = await setup(dir)
    await openGit(t)
    await toCommits(t)
    await press(t, "ESCAPE") // nothing to leave in the commit list itself
    expect(frame(t)).toContain("Commits")

    await press(t, "RETURN") // files
    await settle(t, 300)
    await press(t, "RETURN") // diff
    expect(frame(t)).toContain("j/k scroll") // the diff has the focus
    await press(t, "ESCAPE")
    expect(frame(t)).not.toContain("j/k scroll")
    expect(frame(t)).toContain("Files ·") // back on the file list, still browsing
    await press(t, "j")
    await settle(t, 300)
    expect(frame(t)).toContain("added.txt")

    await press(t, "ESCAPE")
    expect(frame(t)).not.toContain("Files ·")
    expect(frame(t)).toContain("mixed changes") // the commit list is back
  })

  test("esc from the diff goes back to the panel it was opened from", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await press(t, "RETURN") // from Changes
    expect(frame(t)).toContain("j/k scroll")
    await press(t, "ESCAPE")
    expect(frame(t)).not.toContain("j/k scroll")
    expect(frame(t)).toContain("c commit") // Changes has the focus again
    await press(t, "d") // and its keys work: this asks to discard
    expect(frame(t)).toContain("Throw away the changes in b.txt")
  })

  test("esc returns to branches when the diff came from there", async () => {
    const dir = project()
    git(dir, "branch", "other")
    const t = await setup(dir)
    await openGit(t)
    await press(t, "TAB") // branches
    await press(t, "TAB") // commits
    await press(t, "TAB") // stash
    await press(t, "TAB") // diff
    expect(frame(t)).toContain("j/k scroll")
    await press(t, "ESCAPE")
    expect(frame(t)).toContain("space apply") // the stash panel, the last list the diff followed
  })

  test("stashes open to their files too; space applies", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "stashed\n")
    writeFileSync(join(dir, "extra.txt"), "x\n")
    git(dir, "add", "extra.txt")
    git(dir, "stash", "push", "-q", "-m", "wip two files")
    const t = await setup(dir)
    await openGit(t)
    for (let i = 0; i < 3; i++) await press(t, "TAB") // stash
    await settle(t, 300)
    await press(t, "RETURN")
    await settle(t, 300)
    let f = frame(t)
    expect(f).toContain("Files · stash@{0}")
    expect(f).toMatch(/M b\.txt/)
    expect(f).toMatch(/A extra\.txt/)
    await press(t, "j")
    await settle(t, 300)
    expect(frame(t)).toContain("stash@{0} · extra.txt")
    await press(t, "ESCAPE")
    await press(t, " ") // apply
    await settle(t)
    expect(readFileSync(join(dir, "b.txt"), "utf8")).toBe("stashed\n")
  })

  test("zoom shows only the focused panel", async () => {
    const dir = project()
    writeFileSync(join(dir, "b.txt"), "changed\n")
    const t = await setup(dir)
    await openGit(t)
    await press(t, "z")
    expect(frame(t)).toContain("Changes")
    expect(frame(t)).not.toContain("Branches")
    expect(frame(t)).not.toContain("Diff ·")
    await press(t, "ESCAPE")
    expect(frame(t)).toContain("Branches")
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

describe("git view with services in different repos", () => {
  test("the header sums up every repo and the view lists them with branch, changes and services", async () => {
    const ws = workspace()
    writeFileSync(join(ws, "api", "a1.txt"), "1\n")
    writeFileSync(join(ws, "api", "a2.txt"), "2\n")
    writeFileSync(join(ws, "web", "w1.txt"), "1\n")
    const t = await setupWorkspace(ws)
    await settle(t, 600)
    expect(frame(t)).toContain("⎇ 2 repos")
    expect(frame(t)).toContain("✎3 in 2")

    await openGit(t)
    const f = frame(t)
    expect(f).toContain("Repos · 2")
    expect(f).toMatch(/● api\s+main\s+✎2/)
    expect(f).toMatch(/● web\s+develop\s+✎1/)
    expect(f).toContain("api") // services of the selected repo in the panel footer
  })

  test("picking another repo switches every panel to it, and actions only touch that repo", async () => {
    const ws = workspace()
    writeFileSync(join(ws, "api", "a1.txt"), "1\n")
    writeFileSync(join(ws, "web", "w1.txt"), "1\n")
    const t = await setupWorkspace(ws)
    await openGit(t)
    expect(frame(t)).toContain("Diff · a1.txt")
    expect(frame(t)).toContain("api first")

    t.mockInput.pressKey("TAB", { shift: true }) // repos sits right before changes in the tab order
    await Bun.sleep(40)
    await t.renderOnce()
    expect(frame(t)).toContain("pick repo") // the repos panel has the focus
    await press(t, "j")
    await settle(t, 300)
    expect(frame(t)).toContain("Diff · w1.txt")
    expect(frame(t)).toContain("web first")
    expect(frame(t)).toContain("Changes · develop")
    expect(frame(t)).not.toContain("api first")

    await press(t, "RETURN") // back to the changes of web
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

  test("F fetches all of them", async () => {
    const ws = workspace()
    const t = await setupWorkspace(ws)
    await openGit(t)
    await press(t, "F")
    await settle(t, 600)
    expect(frame(t)).toContain("fetched 2 repos")
  })

  test("with a single repo there is no Repos panel and tab never lands on a hidden one", async () => {
    const dir = project()
    const t = await setup(dir)
    await openGit(t)
    expect(frame(t)).not.toContain("Repos")
    t.mockInput.pressKey("TAB", { shift: true }) // backwards from the first panel: must be the diff, not a hidden repos panel
    await Bun.sleep(40)
    await t.renderOnce()
    expect(frame(t)).toContain("stage hunk")
  })

  test("services outside any repo: the view says so", async () => {
    const ws = mkdtempSync(join(tmpdir(), "orbit-norepos-"))
    writeFileSync(join(ws, "orbit.yaml"), "name: n\nservices:\n  s: { cmd: sleep 30, autostart: false }\n")
    const t = await setupWorkspace(ws)
    await press(t, "4")
    expect(frame(t)).toContain("None of this project's services are in a git repository")
  })
})
