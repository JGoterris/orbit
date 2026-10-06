import { exec } from "../exec.ts"
import { GIT_ENV, type FileChange } from "./status.ts"

export interface Hunk {
  /** the `@@ -a,b +c,d @@ context` line */
  header: string
  /** every line after the header, including " ", "+", "-" and "\ No newline" markers */
  lines: string[]
  oldStart: number
  newStart: number
}

export interface DiffFile {
  /** `diff --git`, `index`, `---`, `+++`, mode/rename lines: everything before the first hunk */
  header: string[]
  hunks: Hunk[]
  binary: boolean
  /** path shown for the file (the new one, or the old one for deletions) */
  path: string
}

function filePath(header: string[]): string {
  const plus = header.find((l) => l.startsWith("+++ "))?.slice(4)
  const minus = header.find((l) => l.startsWith("--- "))?.slice(4)
  const pick = plus && plus !== "/dev/null" ? plus : minus
  if (pick && pick !== "/dev/null") return pick.replace(/^[ab]\//, "")
  const m = /^diff --git a\/(.*) b\/(.*)$/.exec(header[0] ?? "")
  return m ? m[2]! : (header[0] ?? "")
}

/** Parses unified diff text (`git diff`, `git show`) into files and hunks. */
export function parseDiff(text: string): DiffFile[] {
  const files: DiffFile[] = []
  let file: DiffFile | undefined
  let hunk: Hunk | undefined
  const lines = text.split("\n")
  if (lines.at(-1) === "") lines.pop()
  for (const line of lines) {
    if (line.startsWith("diff --git ")) {
      file = { header: [line], hunks: [], binary: false, path: "" }
      files.push(file)
      hunk = undefined
    } else if (!file) {
      continue
    } else if (line.startsWith("@@")) {
      const m = /^@@ -(\d+)(?:,\d+)? \+(\d+)/.exec(line)
      hunk = { header: line, lines: [], oldStart: Number(m?.[1] ?? 0), newStart: Number(m?.[2] ?? 0) }
      file.hunks.push(hunk)
    } else if (hunk) {
      hunk.lines.push(line)
    } else {
      if (line.startsWith("Binary files ") || line.startsWith("GIT binary patch")) file.binary = true
      file.header.push(line)
    }
  }
  for (const f of files) f.path = filePath(f.header)
  return files
}

export type DiffSide = "unstaged" | "staged"

const BASE = ["diff", "--no-color", "--no-ext-diff"]

/** The diff of one change: the worktree against the index, or the index against HEAD. */
export async function fileDiff(root: string, f: FileChange, side: DiffSide): Promise<string> {
  if (f.kind === "untracked") {
    // exit code 1 just means "there are differences"
    return (await exec(["git", ...BASE, "--no-index", "--", "/dev/null", f.path], { cwd: root, env: GIT_ENV })).stdout
  }
  const paths = f.orig && side === "staged" ? [f.orig, f.path] : [f.path]
  const args = side === "staged" ? [...BASE, "--cached", "--"] : [...BASE, "--"]
  return (await exec(["git", ...args, ...paths], { cwd: root, env: GIT_ENV })).stdout
}

export async function commitDiff(root: string, rev: string): Promise<string> {
  return (await exec(["git", "show", "--format=", "--no-color", "--no-ext-diff", rev], { cwd: root, env: GIT_ENV })).stdout
}

export async function stashDiff(root: string, ref: string): Promise<string> {
  return (await exec(["git", "stash", "show", "-p", "--no-color", ref], { cwd: root, env: GIT_ENV })).stdout
}

/** Diff between two revisions (`a..b`, a branch name…). */
export async function rangeDiff(root: string, range: string): Promise<string> {
  return (await exec(["git", ...BASE, range], { cwd: root, env: GIT_ENV })).stdout
}
