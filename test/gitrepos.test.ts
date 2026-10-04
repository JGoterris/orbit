import { afterAll, describe, expect, test } from "bun:test"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadConfig } from "../src/config/load.ts"
import { GitRepo } from "../src/core/git/repo.ts"
import { discoverRepos, repoEntries, repoOfService, summarize } from "../src/core/git/repos.ts"

const dirs: string[] = []
afterAll(() => dirs.forEach((d) => rmSync(d, { recursive: true, force: true })))

const git = (cwd: string, ...args: string[]) => Bun.spawnSync(["git", ...args], { cwd })
function repo(dir: string) {
  mkdirSync(dir, { recursive: true })
  git(dir, "init", "-q", "-b", "main")
  git(dir, "config", "user.name", "T")
  git(dir, "config", "user.email", "t@e.com")
  writeFileSync(join(dir, "f.txt"), "x\n")
  git(dir, "add", ".")
  git(dir, "commit", "-qm", "init")
  return dir
}

/** workspace/ (not a repo) with orbit.yaml; api/ and web/ are separate repos, worker/ lives inside api's repo. */
function workspace(yaml: string) {
  const ws = mkdtempSync(join(tmpdir(), "orbit-ws-"))
  dirs.push(ws)
  repo(join(ws, "api"))
  repo(join(ws, "web"))
  mkdirSync(join(ws, "api", "worker"))
  mkdirSync(join(ws, "plain"))
  writeFileSync(join(ws, "orbit.yaml"), yaml)
  return ws
}

describe("discoverRepos", () => {
  test("one repo per distinct service location; services sharing a repo are grouped; folders that are no repo are skipped", () => {
    const ws = workspace(
      [
        "name: ws",
        "services:",
        "  api: { cmd: 'true', cwd: ./api }",
        "  worker: { cmd: 'true', cwd: ./api/worker }",
        "  web: { cmd: 'true', cwd: ./web }",
        "  tool: { cmd: 'true', cwd: ./plain }",
        "",
      ].join("\n"),
    )
    const found = discoverRepos(loadConfig({ dir: ws }))
    expect(found.map((e) => [e.name, e.services])).toEqual([
      ["api", ["api", "worker"]],
      ["web", ["web"]],
    ])
    expect(found[0]!.repo.root).toBe(join(ws, "api"))
    expect(repoOfService(found, "worker")).toBe(0)
    expect(repoOfService(found, "web")).toBe(1)
    expect(repoOfService(found, "tool")).toBe(-1)
  })

  test("the project folder's own repo comes first, and services inside it do not duplicate it", () => {
    const ws = workspace("name: ws\nservices:\n  web: { cmd: 'true', cwd: ./web }\n  top: { cmd: 'true' }\n")
    repo(ws) // the workspace itself becomes a repo (api/ and web/ stay nested repos of their own)
    const found = discoverRepos(loadConfig({ dir: ws }))
    expect(found.map((e) => e.repo.root)).toEqual([ws, join(ws, "web")])
    expect(found[0]!.services).toEqual(["top"])
  })

  test("a project with no repos at all has none", () => {
    const ws = mkdtempSync(join(tmpdir(), "orbit-none-"))
    dirs.push(ws)
    writeFileSync(join(ws, "orbit.yaml"), "name: n\nservices:\n  s: { cmd: 'true' }\n")
    expect(discoverRepos(loadConfig({ dir: ws }))).toEqual([])
  })

  test("two repos with the same folder name get their parent to tell them apart", () => {
    const a = repo(join(mkdtempSync(join(tmpdir(), "orbit-a-")), "app"))
    const b = repo(join(mkdtempSync(join(tmpdir(), "orbit-b-")), "app"))
    dirs.push(a, b)
    const names = repoEntries([new GitRepo(a), new GitRepo(b)]).map((e) => e.name)
    expect(names[0]).not.toBe(names[1])
    expect(names.every((n) => n.endsWith("/app"))).toBe(true)
  })
})

describe("summarize", () => {
  test("adds up dirty repos, changes and ahead/behind across repos", async () => {
    const a = repo(join(mkdtempSync(join(tmpdir(), "orbit-s-")), "a"))
    const b = repo(join(mkdtempSync(join(tmpdir(), "orbit-s-")), "b"))
    dirs.push(a, b)
    writeFileSync(join(a, "f.txt"), "changed\n")
    writeFileSync(join(a, "new.txt"), "n\n")
    const entries = repoEntries([new GitRepo(a), new GitRepo(b)])
    expect(summarize(entries)).toEqual({ repos: 2, dirty: 0, changes: 0, ahead: 0, behind: 0 }) // nothing read yet
    await Promise.all(entries.map((e) => e.repo.refresh()))
    expect(summarize(entries)).toEqual({ repos: 2, dirty: 1, changes: 2, ahead: 0, behind: 0 })
  })
})
