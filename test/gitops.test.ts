import { afterAll, describe, expect, test } from "bun:test"
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { commitDiff, fileDiff, hunkPatch, parseDiff, parseNameStatus, revFileDiff, revFiles } from "../src/core/git/diff.ts"
import * as ops from "../src/core/git/ops.ts"
import { GitRepo } from "../src/core/git/repo.ts"
import { hasStaged, hasUnstaged, parseStatus, readStatus } from "../src/core/git/status.ts"

const dirs: string[] = []
afterAll(() => dirs.forEach((d) => rmSync(d, { recursive: true, force: true })))

const sh = (cwd: string, ...args: string[]) => {
  const r = Bun.spawnSync(["git", "-c", "user.name=Test", "-c", "user.email=t@example.com", ...args], { cwd })
  if (r.exitCode !== 0) throw new Error(`git ${args.join(" ")}: ${r.stderr}`)
  return r.stdout.toString()
}

/** A repo with one commit containing a.txt (10 lines) and b.txt. */
function repo() {
  const dir = mkdtempSync(join(tmpdir(), "orbit-gitops-"))
  dirs.push(dir)
  sh(dir, "init", "-q", "-b", "main")
  sh(dir, "config", "user.name", "Test")
  sh(dir, "config", "user.email", "t@example.com")
  writeFileSync(join(dir, "a.txt"), Array.from({ length: 30 }, (_, i) => `line ${i + 1}`).join("\n") + "\n")
  writeFileSync(join(dir, "b.txt"), "b\n")
  sh(dir, "add", ".")
  sh(dir, "commit", "-q", "-m", "first\n\nbody of first")
  return dir
}
const edit = (dir: string, file: string, fn: (s: string) => string) =>
  writeFileSync(join(dir, file), fn(readFileSync(join(dir, file), "utf8")))

describe("status", () => {
  test("parses branch headers, tracked, renamed, untracked and conflicted entries", () => {
    const out = [
      "# branch.oid 1234567890abcdef",
      "# branch.head main",
      "# branch.upstream origin/main",
      "# branch.ab +2 -1",
      "1 .M N... 100644 100644 100644 aaa bbb my file.txt",
      "2 R. N... 100644 100644 100644 aaa bbb R100 new name.txt",
      "old name.txt",
      "u UU N... 100644 100644 100644 100644 a b c conflict.txt",
      "? new.txt",
      "",
    ].join("\0")
    const st = parseStatus(out)
    expect(st).toMatchObject({ branch: "main", upstream: "origin/main", ahead: 2, behind: 1, hasCommits: true, oid: "1234567" })
    expect(st.files).toEqual([
      { path: "my file.txt", x: ".", y: "M", kind: "tracked" },
      { path: "new name.txt", orig: "old name.txt", x: "R", y: ".", kind: "tracked" },
      { path: "conflict.txt", x: "U", y: "U", kind: "conflict" },
      { path: "new.txt", x: ".", y: "?", kind: "untracked" },
    ])
    expect(hasStaged(st.files[1]!)).toBe(true)
    expect(hasUnstaged(st.files[1]!)).toBe(false)
    expect(hasUnstaged(st.files[3]!)).toBe(true)
  })

  test("detached HEAD and a repo without commits", () => {
    expect(parseStatus("# branch.oid abcdef1234\0# branch.head (detached)\0")).toMatchObject({ branch: undefined, oid: "abcdef1" })
    expect(parseStatus("# branch.oid (initial)\0# branch.head main\0")).toMatchObject({ branch: "main", hasCommits: false })
  })

  test("readStatus on a real repo; undefined outside one", async () => {
    const dir = repo()
    edit(dir, "a.txt", (s) => s.replace("line 1\n", "LINE 1\n"))
    writeFileSync(join(dir, "new.txt"), "x")
    mkdirSync(join(dir, "sub"))
    writeFileSync(join(dir, "sub", "deep.txt"), "x")
    const st = (await readStatus(dir))!
    expect(st.branch).toBe("main")
    expect(st.files.map((f) => `${f.x}${f.y} ${f.path}`).sort()).toEqual([".? new.txt", ".? sub/deep.txt", ".M a.txt"])
    expect(await readStatus(mkdtempSync(join(tmpdir(), "orbit-nogit-")))).toBeUndefined()
  })
})

describe("diff parsing", () => {
  test("files, hunks, binary and no-newline markers", () => {
    const text = [
      "diff --git a/x.ts b/x.ts",
      "index 1..2 100644",
      "--- a/x.ts",
      "+++ b/x.ts",
      "@@ -1,2 +1,2 @@ fn",
      "-old",
      "+new",
      " same",
      "@@ -10 +10 @@",
      "-a",
      "\\ No newline at end of file",
      "+b",
      "diff --git a/img.png b/img.png",
      "Binary files a/img.png and b/img.png differ",
      "",
    ].join("\n")
    const files = parseDiff(text)
    expect(files.map((f) => [f.path, f.hunks.length, f.binary])).toEqual([
      ["x.ts", 2, false],
      ["img.png", 0, true],
    ])
    expect(files[0]!.hunks[1]).toMatchObject({ oldStart: 10, newStart: 10, lines: ["-a", "\\ No newline at end of file", "+b"] })
    expect(hunkPatch(files[0]!, 0)).toBe(
      "diff --git a/x.ts b/x.ts\nindex 1..2 100644\n--- a/x.ts\n+++ b/x.ts\n@@ -1,2 +1,2 @@ fn\n-old\n+new\n same\n",
    )
  })

  test("deleted files take their path from the old side", () => {
    const [f] = parseDiff("diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\n--- a/gone.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n")
    expect(f!.path).toBe("gone.txt")
  })
})

describe("staging", () => {
  test("stage, unstage and discard files, tracked and untracked", async () => {
    const dir = repo()
    edit(dir, "a.txt", (s) => s + "more\n")
    writeFileSync(join(dir, "new.txt"), "n\n")
    let st = (await readStatus(dir))!

    expect((await ops.stage(dir, ["a.txt", "new.txt"])).ok).toBe(true)
    st = (await readStatus(dir))!
    expect(st.files.map((f) => `${f.x}${f.y}`)).toEqual(["M.", "A."])

    expect((await ops.unstage(dir, ["new.txt"], true)).ok).toBe(true)
    expect((await readStatus(dir))!.files.find((f) => f.path === "new.txt")!.kind).toBe("untracked")

    expect((await ops.discard(dir, "new.txt", true)).ok).toBe(true)
    expect(Bun.file(join(dir, "new.txt")).size).toBe(0) // gone
    expect((await ops.unstageAll(dir, true)).ok).toBe(true)
    expect((await ops.discard(dir, "a.txt", false)).ok).toBe(true)
    expect((await readStatus(dir))!.files).toEqual([])
  })

  test("a repo without commits can stage and unstage", async () => {
    const dir = mkdtempSync(join(tmpdir(), "orbit-empty-"))
    dirs.push(dir)
    sh(dir, "init", "-q", "-b", "main")
    writeFileSync(join(dir, "f.txt"), "x\n")
    expect((await ops.stage(dir, ["f.txt"])).ok).toBe(true)
    expect((await readStatus(dir))!.files[0]).toMatchObject({ x: "A", y: "." })
    const res = await ops.unstage(dir, ["f.txt"], false)
    expect(res.ok).toBe(true)
    expect((await readStatus(dir))!.files[0]!.kind).toBe("untracked")
  })

  test("stage, unstage and discard single hunks", async () => {
    const dir = repo()
    // two far-apart changes -> two hunks
    edit(dir, "a.txt", (s) => s.replace("line 2\n", "TWO\n").replace("line 29\n", "TWENTY-NINE\n"))
    const f = (await readStatus(dir))!.files[0]!
    const unstaged = parseDiff(await fileDiff(dir, f, "unstaged"))[0]!
    expect(unstaged.hunks).toHaveLength(2)

    expect((await ops.applyHunk(dir, unstaged, 1, "stage")).ok).toBe(true)
    const staged = parseDiff(await fileDiff(dir, f, "staged"))[0]!
    expect(staged.hunks).toHaveLength(1)
    expect(staged.hunks[0]!.lines).toContain("+TWENTY-NINE")
    expect(parseDiff(await fileDiff(dir, f, "unstaged"))[0]!.hunks).toHaveLength(1)

    expect((await ops.applyHunk(dir, staged, 0, "unstage")).ok).toBe(true)
    expect(parseDiff(await fileDiff(dir, f, "staged"))).toHaveLength(0)

    const again = parseDiff(await fileDiff(dir, f, "unstaged"))[0]!
    expect((await ops.applyHunk(dir, again, 0, "discard")).ok).toBe(true)
    const text = readFileSync(join(dir, "a.txt"), "utf8")
    expect(text).toContain("line 2\n") // first hunk reverted in the worktree
    expect(text).toContain("TWENTY-NINE") // second one untouched
  })

  test("untracked files diff as all-added", async () => {
    const dir = repo()
    writeFileSync(join(dir, "new.txt"), "one\ntwo\n")
    const f = (await readStatus(dir))!.files[0]!
    const [d] = parseDiff(await fileDiff(dir, f, "unstaged"))
    expect(d!.path).toBe("new.txt")
    expect(d!.hunks[0]!.lines).toEqual(["+one", "+two"])
  })
})

describe("commits", () => {
  test("commit, amend keeping the body, reword only the subject, log and commit diff", async () => {
    const dir = repo()
    edit(dir, "b.txt", () => "b2\n")
    await ops.stageAll(dir)
    const res = await ops.commit(dir, "second\n\nwith body")
    expect(res.ok).toBe(true)
    expect(await ops.lastMessage(dir)).toBe("second\n\nwith body")

    edit(dir, "b.txt", () => "b3\n")
    await ops.stageAll(dir)
    expect((await ops.amend(dir)).ok).toBe(true) // --no-edit
    expect(await ops.lastMessage(dir)).toBe("second\n\nwith body")
    expect((await ops.amend(dir, "reworded")).ok).toBe(true)
    expect(await ops.lastMessage(dir)).toBe("reworded\n\nwith body")

    const commits = await ops.log(dir)
    expect(commits.map((c) => c.subject)).toEqual(["reworded", "first"])
    expect(commits[0]).toMatchObject({ author: "Test" })
    expect(commits[0]!.refs).toContain("main")
    const files = parseDiff(await commitDiff(dir, commits[0]!.hash))
    expect(files.map((f) => f.path)).toEqual(["b.txt"])
  })

  test("commit with nothing staged reports git's message", async () => {
    const dir = repo()
    const res = await ops.commit(dir, "nothing")
    expect(res.ok).toBe(false)
    expect(res.message).toContain("nothing")
  })

  test("parseLog skips graph-only lines and keeps subjects containing the separator", () => {
    const S = "\x1f"
    const out = `* ${S}abc1234${S}Ann${S}2 days ago${S}HEAD -> main${S}fix: a${S}b\n|\\  \n| * ${S}def5678${S}Bob${S}3 days ago${S}${S}wip\n`
    const c = ops.parseLog(out)
    expect(c).toHaveLength(2)
    expect(c[0]).toMatchObject({ graph: "* ", hash: "abc1234", subject: `fix: a${S}b`, refs: "HEAD -> main" })
    expect(c[1]).toMatchObject({ graph: "| * ", refs: "" })
  })
})

describe("branches and stash", () => {
  test("create, list, switch and delete branches; unmerged needs force", async () => {
    const dir = repo()
    expect((await ops.createBranch(dir, "feature")).ok).toBe(true)
    edit(dir, "b.txt", () => "feature\n")
    await ops.stageAll(dir)
    await ops.commit(dir, "on feature")
    expect((await ops.checkout(dir, "main")).ok).toBe(true)
    const list = await ops.branches(dir)
    expect(list.map((b) => [b.name, b.current])).toEqual(expect.arrayContaining([["main", true], ["feature", false]]))
    const del = await ops.deleteBranch(dir, "feature")
    expect(del.ok).toBe(false)
    expect(del.message).toContain("not fully merged")
    expect((await ops.deleteBranch(dir, "feature", true)).ok).toBe(true)
    expect((await ops.checkout(dir, "ghost")).ok).toBe(false)
  })

  test("stash push (with untracked), list, apply, pop, drop", async () => {
    const dir = repo()
    edit(dir, "b.txt", () => "changed\n")
    writeFileSync(join(dir, "u.txt"), "u\n")
    expect((await ops.stashPush(dir, "my stash")).ok).toBe(true)
    expect((await readStatus(dir))!.files).toEqual([])
    const [s] = await ops.stashes(dir)
    expect(s).toMatchObject({ ref: "stash@{0}" })
    expect(s!.message).toContain("my stash")
    expect((await ops.stashApply(dir, s!.ref)).ok).toBe(true)
    expect((await readStatus(dir))!.files).toHaveLength(2)
    await ops.discard(dir, "b.txt", false)
    await ops.discard(dir, "u.txt", true)
    expect((await ops.stashPop(dir, s!.ref)).ok).toBe(true)
    expect(await ops.stashes(dir)).toEqual([])
    await ops.stashPush(dir)
    expect((await ops.stashDrop(dir, "stash@{0}")).ok).toBe(true)
    expect(await ops.stashes(dir)).toEqual([])
  })
})

describe("remotes", () => {
  test("fetch, pull and push against a local bare remote", async () => {
    const remote = mkdtempSync(join(tmpdir(), "orbit-remote-"))
    dirs.push(remote)
    sh(remote, "init", "-q", "--bare", "-b", "main")
    const a = repo()
    sh(a, "remote", "add", "origin", remote)
    expect((await ops.push(a, "main", false)).ok).toBe(true) // sets upstream
    expect((await readStatus(a))!.upstream).toBe("origin/main")

    const b = mkdtempSync(join(tmpdir(), "orbit-clone-"))
    dirs.push(b)
    sh(b, "clone", "-q", remote, ".")
    sh(b, "config", "user.name", "T")
    sh(b, "config", "user.email", "t@e.com")
    edit(b, "b.txt", () => "from b\n")
    sh(b, "commit", "-qam", "from b")
    sh(b, "push", "-q")

    expect((await ops.fetch(a)).ok).toBe(true)
    expect(await readStatus(a)).toMatchObject({ ahead: 0, behind: 1 })
    expect((await ops.pull(a)).ok).toBe(true)
    expect(readFileSync(join(a, "b.txt"), "utf8")).toBe("from b\n")
    expect(await readStatus(a)).toMatchObject({ ahead: 0, behind: 0 })
    expect((await ops.push(a, "main", true)).ok).toBe(true)
  })

  test("a failing remote reports instead of hanging", async () => {
    const a = repo()
    sh(a, "remote", "add", "origin", "/definitely/not/a/repo")
    const res = await ops.fetch(a)
    expect(res.ok).toBe(false)
    expect(res.message).not.toBe("")
  })
})

describe("GitRepo", () => {
  test("emits change only when something changed, and run() refreshes everything", async () => {
    const dir = repo()
    const gr = new GitRepo(dir)
    let changes = 0
    gr.on("change", () => changes++)
    await gr.refresh("all")
    expect(changes).toBe(1)
    expect(gr.status?.branch).toBe("main")
    expect(gr.commits.map((c) => c.subject)).toEqual(["first"])
    expect(gr.branches.map((b) => b.name)).toEqual(["main"])

    await gr.refresh()
    expect(changes).toBe(1) // identical: no event

    edit(dir, "b.txt", () => "x\n")
    const res = await gr.run(() => ops.stageAll(dir))
    expect(res.ok).toBe(true)
    expect(changes).toBe(2)
    expect(gr.status?.files[0]).toMatchObject({ x: "M", y: "." })
  })

  test("concurrent refreshes coalesce, and find() only finds repos", async () => {
    const dir = repo()
    const gr = new GitRepo(dir)
    await Promise.all([gr.refresh(), gr.refresh("all"), gr.refresh()])
    await gr.refresh()
    expect(gr.commits).toHaveLength(1) // the "all" request was honoured
    expect(GitRepo.find(dir)?.root).toBe(dir)
    expect(GitRepo.find(mkdtempSync(join(tmpdir(), "orbit-plain-")))).toBeUndefined()
  })
})

describe("files of a commit or stash", () => {
  test("parseNameStatus handles plain entries and renames/copies", () => {
    expect(parseNameStatus("M\0a.txt\0A\0new file.txt\0R100\0old.txt\0new.txt\0D\0gone.txt\0")).toEqual([
      { status: "M", path: "a.txt" },
      { status: "A", path: "new file.txt" },
      { status: "R", orig: "old.txt", path: "new.txt" },
      { status: "D", path: "gone.txt" },
    ])
    expect(parseNameStatus("")).toEqual([])
  })

  test("lists what a commit changed and diffs one file at a time", async () => {
    const dir = repo()
    edit(dir, "a.txt", (t) => t.replace("line 1\n", "ONE\n"))
    writeFileSync(join(dir, "added.txt"), "new\n")
    sh(dir, "mv", "b.txt", "renamed.txt")
    sh(dir, "add", "-A")
    sh(dir, "commit", "-q", "-m", "mixed")
    const [head] = await ops.log(dir)
    const files = await revFiles(dir, head!.hash)
    expect(files.map((f) => `${f.status} ${f.orig ? f.orig + " -> " : ""}${f.path}`).sort()).toEqual(["A added.txt", "M a.txt", "R b.txt -> renamed.txt"])

    const a = files.find((f) => f.path === "a.txt")!
    const only = parseDiff(await revFileDiff(dir, head!.hash, a))
    expect(only.map((f) => f.path)).toEqual(["a.txt"])
    expect(only[0]!.hunks[0]!.lines).toContain("+ONE")

    const added = parseDiff(await revFileDiff(dir, head!.hash, files.find((f) => f.path === "added.txt")!))
    expect(added[0]!.hunks[0]!.lines).toEqual(["+new"])
    const renamed = files.find((f) => f.status === "R")!
    expect(parseDiff(await revFileDiff(dir, head!.hash, renamed))).toHaveLength(1)
  })

  test("a root commit lists all its files, and deletions show up", async () => {
    const dir = repo()
    const [first] = await ops.log(dir)
    expect((await revFiles(dir, first!.hash)).map((f) => `${f.status} ${f.path}`).sort()).toEqual(["A a.txt", "A b.txt"])
    sh(dir, "rm", "-q", "b.txt")
    sh(dir, "commit", "-q", "-m", "drop b")
    const [head] = await ops.log(dir)
    const [gone] = await revFiles(dir, head!.hash)
    expect(gone).toEqual({ status: "D", path: "b.txt" })
    expect(parseDiff(await revFileDiff(dir, head!.hash, gone!))[0]!.hunks[0]!.lines).toEqual(["-b"])
  })

  test("a merge commit is compared with its first parent", async () => {
    const dir = repo()
    sh(dir, "switch", "-q", "-c", "side")
    writeFileSync(join(dir, "side.txt"), "s\n")
    sh(dir, "add", ".")
    sh(dir, "commit", "-q", "-m", "side work")
    sh(dir, "switch", "-q", "main")
    sh(dir, "merge", "-q", "--no-ff", "-m", "merge side", "side")
    const [merge] = await ops.log(dir)
    expect(merge!.subject).toBe("merge side")
    expect((await revFiles(dir, merge!.hash)).map((f) => f.path)).toEqual(["side.txt"])
  })

  test("a stash lists its files by hash, even after another stash shifts stash@{n}", async () => {
    const dir = repo()
    edit(dir, "b.txt", () => "one\n")
    await ops.stashPush(dir, "first")
    edit(dir, "a.txt", (t) => t + "x\n")
    await ops.stashPush(dir, "second")
    const list = await ops.stashes(dir)
    expect(list.map((s) => s.message.includes("first"))).toEqual([false, true]) // newest first
    const first = list[1]!
    expect(first.hash).toMatch(/^[0-9a-f]{40}$/)
    expect((await revFiles(dir, first.hash)).map((f) => f.path)).toEqual(["b.txt"])
    expect((await revFiles(dir, list[0]!.hash)).map((f) => f.path)).toEqual(["a.txt"])
  })
})
