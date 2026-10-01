import { afterEach, describe, expect, test } from "bun:test"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { findGitRoot } from "../src/core/git.ts"

let tmp: string | undefined
afterEach(() => tmp && rmSync(tmp, { recursive: true, force: true }))

function makeTmp() {
  tmp = mkdtempSync(join(tmpdir(), "orbit-git-"))
  return tmp
}

describe("findGitRoot", () => {
  test("finds the repo from a subfolder", () => {
    const root = makeTmp()
    mkdirSync(join(root, ".git"))
    mkdirSync(join(root, "packages/api"), { recursive: true })
    expect(findGitRoot(join(root, "packages/api"))).toBe(root)
  })

  test("accepts .git as a file (worktrees, submodules)", () => {
    const root = makeTmp()
    writeFileSync(join(root, ".git"), "gitdir: /elsewhere")
    expect(findGitRoot(root)).toBe(root)
  })

  test("returns undefined outside a repo", () => {
    expect(findGitRoot(makeTmp())).toBeUndefined()
  })
})
