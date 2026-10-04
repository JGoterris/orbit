import type { DiffFile } from "../core/git/diff.ts"

export type RowKind = "file" | "note" | "hunk" | "add" | "del" | "ctx" | "marker"

export interface DiffRow {
  kind: RowKind
  text: string
  /** index into `DiffRows.hunks` this row belongs to, -1 for file rows and notes */
  hunk: number
  /** line numbers in the old / new file (only for ctx, add and del rows) */
  oldNo?: number
  newNo?: number
}

export interface HunkRef {
  /** index of the file in the parsed list, and of the hunk inside it */
  file: number
  index: number
  /** row of its `@@` header */
  row: number
}

export interface DiffRows {
  rows: DiffRow[]
  hunks: HunkRef[]
}

function title(f: DiffFile): string {
  const rename = f.header.find((l) => l.startsWith("rename from "))?.slice(12)
  const flag = f.header.find((l) => l.startsWith("new file mode")) ? " (new)" : f.header.find((l) => l.startsWith("deleted file mode")) ? " (deleted)" : ""
  return rename ? `${rename} → ${f.path}` : `${f.path}${flag}`
}

/** Flattens parsed files into display rows with line numbers, plus where each hunk starts. */
export function buildRows(files: DiffFile[]): DiffRows {
  const rows: DiffRow[] = []
  const hunks: HunkRef[] = []
  files.forEach((f, fi) => {
    rows.push({ kind: "file", text: title(f), hunk: -1 })
    if (f.binary) rows.push({ kind: "note", text: "binary file", hunk: -1 })
    else if (!f.hunks.length) rows.push({ kind: "note", text: f.header.some((l) => l.startsWith("rename ")) ? "renamed, no content changes" : "no content changes (mode or empty file)", hunk: -1 })
    f.hunks.forEach((h, hi) => {
      const id = hunks.length
      hunks.push({ file: fi, index: hi, row: rows.length })
      rows.push({ kind: "hunk", text: h.header, hunk: id })
      let o = h.oldStart
      let n = h.newStart
      for (const line of h.lines) {
        const c = line[0]
        const text = line.slice(1)
        if (c === "+") rows.push({ kind: "add", text, hunk: id, newNo: n++ })
        else if (c === "-") rows.push({ kind: "del", text, hunk: id, oldNo: o++ })
        else if (c === "\\") rows.push({ kind: "marker", text: line, hunk: id })
        else rows.push({ kind: "ctx", text, hunk: id, oldNo: o++, newNo: n++ })
      }
    })
  })
  return { rows, hunks }
}

/** The hunk the view is "on": the last one whose header is at or above the first visible row (the first one before any). */
export function hunkAt(hunks: readonly HunkRef[], scroll: number): number {
  let cur = hunks.length ? 0 : -1
  for (let i = 0; i < hunks.length; i++) if (hunks[i]!.row <= scroll) cur = i
  return cur
}

/** The hunk to act on: the explicitly chosen one while any of it is on screen, else the one at the top of the view. */
export function currentHunk(rows: DiffRows, chosen: number, top: number, page: number): number {
  if (!rows.hunks.length) return -1
  const h = rows.hunks[chosen]
  const end = rows.hunks[chosen + 1]?.row ?? rows.rows.length
  if (h && h.row < top + page && end > top) return chosen
  return hunkAt(rows.hunks, top)
}

/** Scroll position that makes hunk `i` visible: unchanged if it already is, else its header goes to the top. */
export function revealHunk(rows: DiffRows, i: number, top: number, page: number): number {
  const h = rows.hunks[i]
  if (!h) return top
  const end = rows.hunks[i + 1]?.row ?? rows.rows.length
  return h.row >= top && end <= top + page ? top : h.row
}
