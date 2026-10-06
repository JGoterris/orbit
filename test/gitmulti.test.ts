import { afterAll, describe, expect, test } from "bun:test"
import { mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import * as ops from "../src/core/git/ops.ts"
import { branchSpread, runOnRepos, summarizeResults } from "../src/core/git/multi.ts"
import { GitRepo } from "../src/core/git/repo.ts"
import { repoEntries } from "../src/core/git/repos.ts"

const dirs: string[] = []
afterAll(() => dirs.forEach((d) => rmSync(d, { recursive: true, force: true })))

const sh = (cwd: string, ...args: string[]) => {
  const r = Bun.spawnSync(["git", "-c", "user.name=Test", "-c", "user.email=t@example.com", ...args], { cwd })
  if (r.exitCode !== 0) throw new Error(`git ${args.join(" ")}: ${r.stderr}`)
  return r.stdout.toString()
}

function repo(name: string) {
  const dir = mkdtempSync(join(tmpdir(), `orbit-multi-${name}-`))
  dirs.push(dir)
  sh(dir, "init", "-q", "-b", "main")
  writeFileSync(join(dir, "a.txt"), "a\n")
  sh(dir, "add", ".")
  sh(dir, "commit", "-q", "-m", "first")
  return dir
}

async function entries(n: number) {
  const list = repoEntries(Array.from({ length: n }, (_, i) => new GitRepo(repo(`r${i}`))))
  await Promise.all(list.map((e) => e.repo.refresh("all")))
  return list
}

describe("multi-repo", () => {
  test("runOnRepos creates a branch in every repo and refreshes them", async () => {
    const list = await entries(3)
    const rs = await runOnRepos(list, (e) => ops.createBranch(e.repo.root, "feat/x"))
    expect(rs.every((r) => r.res.ok)).toBe(true)
    expect(list.map((e) => e.repo.status?.branch)).toEqual(["feat/x", "feat/x", "feat/x"])
  })

  test("one failing repo does not stop the others, and the summary names it", async () => {
    const list = await entries(3)
    sh(list[1]!.repo.root, "branch", "other") // only the middle one has it
    const rs = await runOnRepos(list, (e) => ops.checkout(e.repo.root, "other"))
    expect(rs.map((r) => r.res.ok)).toEqual([false, true, false])
    expect(list[1]!.repo.status?.branch).toBe("other")
    const sum = summarizeResults("switched", rs)
    expect(sum.ok).toBe(false)
    expect(sum.message).toMatch(/^switched 1\/3 · /)
    expect(summarizeResults("pulled", rs.filter((r) => r.res.ok))).toEqual({ ok: true, message: "pulled" })
  })

  test("branchSpread counts the branches, most common first", async () => {
    const list = await entries(3)
    await runOnRepos([list[2]!], (e) => ops.createBranch(e.repo.root, "dev"))
    expect(branchSpread(list)).toEqual([
      { name: "main", count: 2 },
      { name: "dev", count: 1 },
    ])
  })
})
