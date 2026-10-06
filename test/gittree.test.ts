import { describe, expect, test } from "bun:test"
import { buildTree } from "../src/core/git/tree.ts"
import type { FileChange } from "../src/core/git/status.ts"

const f = (path: string): FileChange => ({ path, x: ".", y: "M", kind: "tracked" })
const shape = (rows: ReturnType<typeof buildTree>) => rows.map((r) => `${r.depth}${r.kind === "dir" ? "d" : "f"}:${r.kind === "dir" ? r.name : r.name}`)

describe("changes tree", () => {
  test("folders first, then files, each sorted; root files at depth 0", () => {
    const rows = buildTree([f("z.txt"), f("src/b.ts"), f("src/a.ts"), f("README.md"), f("docs/x.md")])
    expect(shape(rows)).toEqual(["0d:docs", "1f:x.md", "0d:src", "1f:a.ts", "1f:b.ts", "0f:README.md", "0f:z.txt"])
  })

  test("a folder whose only child is a folder is merged with it", () => {
    const rows = buildTree([f("src/ui/views/Git.tsx"), f("src/ui/views/index.tsx")])
    expect(shape(rows)).toEqual(["0d:src/ui/views", "1f:Git.tsx", "1f:index.tsx"])
    expect(rows[0]).toMatchObject({ kind: "dir", path: "src/ui/views" })
  })

  test("merging stops where a folder has files of its own or several folders", () => {
    const rows = buildTree([f("a/b/c.txt"), f("a/d.txt"), f("a/e/f/g.txt")])
    expect(shape(rows)).toEqual(["0d:a", "1d:b", "2f:c.txt", "1d:e/f", "2f:g.txt", "1f:d.txt"])
  })

  test("a folder counts every change below it, and folding hides them", () => {
    const files = [f("a/b/c.txt"), f("a/d.txt"), f("x.txt")]
    const open = buildTree(files)
    expect(open[0]).toMatchObject({ kind: "dir", path: "a", collapsed: false })
    expect(open[0]!.kind === "dir" && open[0]!.files.map((c) => c.path).sort()).toEqual(["a/b/c.txt", "a/d.txt"])
    const closed = buildTree(files, new Set(["a"]))
    expect(shape(closed)).toEqual(["0d:a", "0f:x.txt"])
    expect(closed[0]).toMatchObject({ collapsed: true })
    expect(buildTree([])).toEqual([])
  })
})
