import { exec, type ExecResult } from "../exec.ts"
import { cmdLog } from "./cmdlog.ts"
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

/** Every operation goes through here, so each one lands in the command log. */
async function git(root: string, args: string[], opts: { input?: string; detached?: boolean } = {}): Promise<OpResult> {
  const at = new Date()
  const res = toResult(await exec(["git", ...args], { cwd: root, env: GIT_ENV, ...opts }))
  cmdLog.add({ root, args, ok: res.ok, message: res.message, at, ms: Date.now() - at.getTime() })
  return res
}

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

// ---------------------------------------------------------------- commits

export function commit(root: string, message: string) {
  return git(root, ["commit", "-F", "-"], { input: message + "\n" })
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
  /** relative date of the last commit */
  when: string
}

export function parseBranches(out: string): BranchInfo[] {
  return out
    .split("\n")
    .filter(Boolean)
    .map((l) => {
      const [head, name, upstream, track, when] = l.split(SEP)
      return { name: name!, current: head === "*", upstream: upstream || undefined, track: (track ?? "").replace(/[[\]]/g, ""), when: when ?? "" }
    })
}

export async function branches(root: string): Promise<BranchInfo[]> {
  const r = await exec(
    ["git", "for-each-ref", "--sort=-committerdate", `--format=%(HEAD)${SEP}%(refname:short)${SEP}%(upstream:short)${SEP}%(upstream:track)${SEP}%(committerdate:relative)`, "refs/heads"],
    { cwd: root, env: GIT_ENV },
  )
  return r.code === 0 ? parseBranches(r.stdout) : []
}

export const checkout = (root: string, branch: string) => git(root, ["switch", branch])
export const createBranch = (root: string, name: string) => git(root, ["switch", "-c", name])
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
