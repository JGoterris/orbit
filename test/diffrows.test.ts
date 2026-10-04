import { describe, expect, test } from "bun:test"
import { parseDiff } from "../src/core/git/diff.ts"
import { buildRows, currentHunk, hunkAt, revealHunk } from "../src/ui/diffRows.ts"

const text = [
  "diff --git a/x.ts b/x.ts",
  "--- a/x.ts",
  "+++ b/x.ts",
  "@@ -5,3 +5,3 @@",
  " keep",
  "-old",
  "+new",
  "@@ -20,1 +20,2 @@",
  "-z",
  "\\ No newline at end of file",
  "+y",
  "+w",
  "diff --git a/old.txt b/new.txt",
  "similarity index 100%",
  "rename from old.txt",
  "rename to new.txt",
  "diff --git a/i.png b/i.png",
  "Binary files a/i.png and b/i.png differ",
  "diff --git a/n.txt b/n.txt",
  "new file mode 100644",
  "--- /dev/null",
  "+++ b/n.txt",
  "@@ -0,0 +1 @@",
  "+hello",
  "",
].join("\n")

describe("buildRows", () => {
  const { rows, hunks } = buildRows(parseDiff(text))

  test("numbers lines on both sides and keeps the no-newline marker out of the count", () => {
    const x = rows.slice(0, 12)
    expect(x.map((r) => [r.kind, r.oldNo, r.newNo])).toEqual([
      ["file", undefined, undefined],
      ["hunk", undefined, undefined],
      ["ctx", 5, 5],
      ["del", 6, undefined],
      ["add", undefined, 6],
      ["hunk", undefined, undefined],
      ["del", 20, undefined],
      ["marker", undefined, undefined],
      ["add", undefined, 20],
      ["add", undefined, 21],
      ["file", undefined, undefined],
      ["note", undefined, undefined],
    ])
  })

  test("titles and notes for renames, binaries and new files", () => {
    expect(rows.filter((r) => r.kind === "file").map((r) => r.text)).toEqual(["x.ts", "old.txt → new.txt", "i.png", "n.txt (new)"])
    expect(rows.filter((r) => r.kind === "note").map((r) => r.text)).toEqual(["renamed, no content changes", "binary file"])
  })

  test("hunk refs point at their header rows across files", () => {
    expect(hunks.map((h) => [h.file, h.index])).toEqual([[0, 0], [0, 1], [3, 0]])
    for (const h of hunks) expect(rows[h.row]!.kind).toBe("hunk")
    expect(rows.filter((r) => r.hunk === 1).length).toBe(5)
  })

  test("hunkAt follows the scroll position", () => {
    const [, b] = hunks.map((h) => h.row) as [number, number, number]
    expect(hunkAt(hunks, 0)).toBe(0) // before any header: the first one
    expect(hunkAt(hunks, b)).toBe(1)
    expect(hunkAt(hunks, b + 1)).toBe(1)
    expect(hunkAt([], 3)).toBe(-1)
  })

  test("currentHunk keeps the chosen hunk while it is on screen, then follows the top of the view", () => {
    const d = buildRows(parseDiff(text))
    const total = d.rows.length
    // everything fits: the chosen hunk stays chosen even though nothing can scroll
    expect(currentHunk(d, 2, 0, total)).toBe(2)
    expect(currentHunk(d, 1, 0, total)).toBe(1)
    // a small window at the top that does not show hunk 2 falls back to what is at the top
    expect(currentHunk(d, 2, 0, 4)).toBe(0)
    expect(currentHunk(d, 0, d.hunks[2]!.row, 3)).toBe(2)
    expect(currentHunk({ rows: [], hunks: [] }, 0, 0, 10)).toBe(-1)
    expect(currentHunk(d, 99, 0, total)).toBe(0) // stale index after hunks disappeared
  })

  test("revealHunk only scrolls when the hunk is not fully visible", () => {
    const d = buildRows(parseDiff(text))
    const total = d.rows.length
    expect(revealHunk(d, 1, 0, total)).toBe(0) // already visible
    expect(revealHunk(d, 1, 0, d.hunks[1]!.row + 2)).toBe(d.hunks[1]!.row) // cut off at the bottom: header to the top
    expect(revealHunk(d, 0, d.hunks[2]!.row, 5)).toBe(d.hunks[0]!.row) // above the view
    expect(revealHunk(d, 7, 3, 5)).toBe(3) // no such hunk: unchanged
  })
})
