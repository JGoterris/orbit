import { exec } from "../exec.ts"

export type GitKind = "tracked" | "untracked" | "conflict"

/** One path in `git status`. `x` is the index column, `y` the worktree column ("." = unchanged). */
export interface FileChange {
  path: string
  /** the path before a rename/copy */
  orig?: string
  x: string
  y: string
  kind: GitKind
}

export interface GitStatus {
  /** current branch, or undefined when HEAD is detached */
  branch?: string
  /** short commit id when detached */
  oid?: string
  upstream?: string
  ahead: number
  behind: number
  /** false in a repo without commits yet */
  hasCommits: boolean
  files: FileChange[]
}

export const hasStaged = (f: FileChange) => f.kind === "tracked" && f.x !== "."
export const hasUnstaged = (f: FileChange) => f.kind === "untracked" || f.kind === "conflict" || f.y !== "."

/** Parses `git status --porcelain=v2 --branch -z` output. */
export function parseStatus(out: string): GitStatus {
  const st: GitStatus = { ahead: 0, behind: 0, hasCommits: true, files: [] }
  const recs = out.split("\0")
  for (let i = 0; i < recs.length; i++) {
    const r = recs[i]!
    if (!r) continue
    if (r.startsWith("# ")) {
      const [key, ...rest] = r.slice(2).split(" ")
      const value = rest.join(" ")
      if (key === "branch.head") st.branch = value === "(detached)" ? undefined : value
      else if (key === "branch.oid") {
        st.hasCommits = value !== "(initial)"
        st.oid = value === "(initial)" ? undefined : value.slice(0, 7)
      } else if (key === "branch.upstream") st.upstream = value
      else if (key === "branch.ab") {
        const m = /^\+(\d+) -(\d+)$/.exec(value)
        if (m) [st.ahead, st.behind] = [Number(m[1]), Number(m[2])]
      }
    } else if (r[0] === "?") {
      st.files.push({ path: r.slice(2), x: ".", y: "?", kind: "untracked" })
    } else if (r[0] === "1") {
      const f = r.split(" ")
      st.files.push({ path: f.slice(8).join(" "), x: f[1]![0]!, y: f[1]![1]!, kind: "tracked" })
    } else if (r[0] === "2") {
      const f = r.split(" ")
      st.files.push({ path: f.slice(9).join(" "), orig: recs[++i], x: f[1]![0]!, y: f[1]![1]!, kind: "tracked" })
    } else if (r[0] === "u") {
      const f = r.split(" ")
      st.files.push({ path: f.slice(10).join(" "), x: f[1]![0]!, y: f[1]![1]!, kind: "conflict" })
    }
  }
  return st
}

/** Env for every git call: no prompts, no optional index locks (orbit polls), stable English output. */
export const GIT_ENV = { ...process.env, GIT_TERMINAL_PROMPT: "0", GIT_OPTIONAL_LOCKS: "0", LC_ALL: "C" } as Record<string, string>

export async function readStatus(root: string): Promise<GitStatus | undefined> {
  const res = await exec(["git", "status", "--porcelain=v2", "--branch", "--untracked-files=all", "-z"], { cwd: root, env: GIT_ENV })
  return res.code === 0 ? parseStatus(res.stdout) : undefined
}
