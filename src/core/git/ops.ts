import { exec, type ExecResult } from "../exec.ts"
import { hunkPatch, type DiffFile } from "./diff.ts"
import { GIT_ENV } from "./status.ts"

export interface OpResult {
  ok: boolean
  /** git's own message: the first useful line of stderr/stdout */
  message: string
}

/** git's own words: the first useful stderr line, else the last stdout line ("nothing to commit…" comes last). */
const first = (r: ExecResult) => {
  const useful = (s: string) => s.split("\n").map((l) => l.trim()).filter((l) => l && !l.startsWith("hint:"))
  return useful(r.stderr)[0] ?? useful(r.stdout).at(-1) ?? ""
}

function toResult(r: ExecResult): OpResult {
  return { ok: r.code === 0, message: first(r) }
}

const git = (root: string, args: string[], opts: { input?: string; detached?: boolean } = {}) =>
  exec(["git", ...args], { cwd: root, env: GIT_ENV, ...opts }).then(toResult)

// ---------------------------------------------------------------- staging

export const stage = (root: string, paths: string[]) => git(root, ["add", "--", ...paths])
export const stageAll = (root: string) => git(root, ["add", "-A"])

export async function unstage(root: string, paths: string[], hasCommits: boolean) {
  // a repo without commits has no HEAD to restore from
  return hasCommits ? git(root, ["restore", "--staged", "--", ...paths]) : git(root, ["rm", "--cached", "-r", "-q", "--", ...paths])
}

export async function unstageAll(root: string, hasCommits: boolean) {
  return hasCommits ? git(root, ["reset", "-q"]) : git(root, ["rm", "--cached", "-r", "-q", "."])
}

/** Throws the worktree changes of a path away (tracked: back to the index; untracked: deleted). */
export function discard(root: string, path: string, untracked: boolean) {
  return untracked ? git(root, ["clean", "-fdq", "--", path]) : git(root, ["restore", "--", path])
}

/** Applies one hunk: to the index (stage), reversed to the index (unstage) or reversed to the worktree (discard). */
export function applyHunk(root: string, file: DiffFile, index: number, mode: "stage" | "unstage" | "discard") {
  const args = ["apply", "--recount", "--whitespace=nowarn"]
  if (mode !== "discard") args.push("--cached")
  if (mode !== "stage") args.push("--reverse")
  return git(root, [...args, "-"], { input: hunkPatch(file, index) })
}

// ---------------------------------------------------------------- commits

export async function lastMessage(root: string): Promise<string> {
  const r = await exec(["git", "log", "-1", "--format=%B"], { cwd: root, env: GIT_ENV })
  return r.code === 0 ? r.stdout.replace(/\s+$/, "") : ""
}

export function commit(root: string, message: string) {
  return git(root, ["commit", "-F", "-"], { input: message + "\n" })
}

/** Amends HEAD. With a `subject` only the first line of the message changes (the body is kept). */
export async function amend(root: string, subject?: string) {
  if (subject === undefined) return git(root, ["commit", "--amend", "--no-edit"])
  const body = (await lastMessage(root)).split("\n").slice(1).join("\n")
  return git(root, ["commit", "--amend", "-F", "-"], { input: subject + (body ? `\n${body}` : "") + "\n" })
}

export interface CommitInfo {
  /** `--graph` drawing in front of the commit (empty when there is none) */
  graph: string
  hash: string
  author: string
  when: string
  refs: string
  subject: string
}

const SEP = "\x1f"

/** Parses `git log --graph --format=\x1f%h…` lines; pure connector lines are skipped. */
export function parseLog(out: string): CommitInfo[] {
  const commits: CommitInfo[] = []
  for (const line of out.split("\n")) {
    const at = line.indexOf(SEP)
    if (at === -1) continue
    const [hash, author, when, refs, ...subject] = line.slice(at + 1).split(SEP)
    commits.push({ graph: line.slice(0, at), hash: hash!, author: author!, when: when!, refs: refs!, subject: subject.join(SEP) })
  }
  return commits
}

export async function log(root: string, limit = 200): Promise<CommitInfo[]> {
  const r = await exec(
    ["git", "log", "--graph", "--decorate=short", `-n${limit}`, `--format=${SEP}%h${SEP}%an${SEP}%ar${SEP}%D${SEP}%s`],
    { cwd: root, env: GIT_ENV },
  )
  return r.code === 0 ? parseLog(r.stdout) : []
}

// ---------------------------------------------------------------- branches

export interface BranchInfo {
  name: string
  current: boolean
  upstream?: string
  /** "ahead 1", "behind 2", "gone"… as git prints it */
  track: string
}

export function parseBranches(out: string): BranchInfo[] {
  return out
    .split("\n")
    .filter(Boolean)
    .map((l) => {
      const [head, name, upstream, track] = l.split(SEP)
      return { name: name!, current: head === "*", upstream: upstream || undefined, track: (track ?? "").replace(/[[\]]/g, "") }
    })
}

export async function branches(root: string): Promise<BranchInfo[]> {
  const r = await exec(
    ["git", "for-each-ref", "--sort=-committerdate", `--format=%(HEAD)${SEP}%(refname:short)${SEP}%(upstream:short)${SEP}%(upstream:track)`, "refs/heads"],
    { cwd: root, env: GIT_ENV },
  )
  return r.code === 0 ? parseBranches(r.stdout) : []
}

export const checkout = (root: string, branch: string) => git(root, ["switch", branch])
export const createBranch = (root: string, name: string) => git(root, ["switch", "-c", name])
/** `force` deletes unmerged branches too (`-D`). */
export const deleteBranch = (root: string, name: string, force = false) => git(root, ["branch", force ? "-D" : "-d", name])

// ---------------------------------------------------------------- remotes

export const fetch = (root: string) => git(root, ["fetch", "--prune"], { detached: true })
export const pull = (root: string) => git(root, ["pull", "--ff-only"], { detached: true })
export function push(root: string, branch: string, hasUpstream: boolean) {
  return git(root, hasUpstream ? ["push"] : ["push", "-u", "origin", branch], { detached: true })
}

// ---------------------------------------------------------------- stash

export interface StashInfo {
  ref: string
  /** the stash commit: stable, unlike `stash@{n}`, which shifts when a new stash is made */
  hash: string
  message: string
}

export function parseStashes(out: string): StashInfo[] {
  return out
    .split("\n")
    .filter(Boolean)
    .map((l) => {
      const [ref, hash, ...msg] = l.split(SEP)
      return { ref: ref!, hash: hash!, message: msg.join(SEP) }
    })
}

export async function stashes(root: string): Promise<StashInfo[]> {
  const r = await exec(["git", "stash", "list", `--format=%gd${SEP}%H${SEP}%s`], { cwd: root, env: GIT_ENV })
  return r.code === 0 ? parseStashes(r.stdout) : []
}

export const stashPush = (root: string, message?: string) =>
  git(root, ["stash", "push", "--include-untracked", ...(message ? ["-m", message] : [])])
export const stashApply = (root: string, ref: string) => git(root, ["stash", "apply", ref])
export const stashPop = (root: string, ref: string) => git(root, ["stash", "pop", ref])
export const stashDrop = (root: string, ref: string) => git(root, ["stash", "drop", ref])
