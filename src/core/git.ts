import { existsSync } from "node:fs"
import { dirname, join, resolve } from "node:path"

/** Nearest ancestor of `dir` (inclusive) containing a `.git` entry (dir or file: worktrees/submodules). */
export function findGitRoot(dir: string): string | undefined {
  let cur = resolve(dir)
  for (;;) {
    if (existsSync(join(cur, ".git"))) return cur
    const parent = dirname(cur)
    if (parent === cur) return
    cur = parent
  }
}
