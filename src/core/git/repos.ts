import { basename, dirname } from "node:path"
import type { OrbitConfig } from "../../config/schema.ts"
import { findGitRoot } from "../git.ts"
import { GitRepo } from "./repo.ts"

/** A repository that some part of the project lives in. */
export interface RepoEntry {
  repo: GitRepo
  /** short label: the folder name (with its parent when two repos share a name) */
  name: string
  /** services whose working directory is inside this repo */
  services: string[]
}

/**
 * The repos of a project. A project is a set of services and each may live in a different repo (or several
 * in the same one), so they come from the services' working directories; the project folder's own repo, if
 * it has one, goes first. Each repo appears once.
 */
export function discoverRepos(config: OrbitConfig): RepoEntry[] {
  const byRoot = new Map<string, RepoEntry>()
  const add = (root: string | undefined, service?: string) => {
    if (!root) return
    let e = byRoot.get(root)
    if (!e) byRoot.set(root, (e = { repo: new GitRepo(root), name: basename(root), services: [] }))
    if (service && !e.services.includes(service)) e.services.push(service)
  }
  add(findGitRoot(config.root))
  for (const svc of Object.values(config.services)) add(findGitRoot(svc.cwd), svc.name)
  return nameRepos([...byRoot.values()])
}

/** Folder names are labels; two repos called the same get their parent folder to tell them apart. */
function nameRepos(entries: RepoEntry[]): RepoEntry[] {
  const count = new Map<string, number>()
  for (const e of entries) count.set(e.name, (count.get(e.name) ?? 0) + 1)
  for (const e of entries) if (count.get(e.name)! > 1) e.name = `${basename(dirname(e.repo.root))}/${e.name}`
  return entries
}

/** Wraps repos handed in directly (tests, embedding) as entries. */
export function repoEntries(repos: GitRepo[]): RepoEntry[] {
  return nameRepos(repos.map((repo) => ({ repo, name: basename(repo.root), services: [] })))
}

export interface RepoSummary {
  repos: number
  /** repos with uncommitted changes */
  dirty: number
  /** changed paths across all repos */
  changes: number
  ahead: number
  behind: number
}

export function summarize(entries: RepoEntry[]): RepoSummary {
  const s: RepoSummary = { repos: entries.length, dirty: 0, changes: 0, ahead: 0, behind: 0 }
  for (const { repo } of entries) {
    const st = repo.status
    if (!st) continue
    if (st.files.length) s.dirty++
    s.changes += st.files.length
    s.ahead += st.ahead
    s.behind += st.behind
  }
  return s
}

/** Index of the repo holding `service`, or -1. */
export const repoOfService = (entries: RepoEntry[], service: string) => entries.findIndex((e) => e.services.includes(service))
