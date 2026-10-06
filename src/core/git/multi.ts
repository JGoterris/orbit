import type { OpResult } from "./ops.ts"
import type { RepoEntry } from "./repos.ts"

export interface RepoOpResult {
  entry: RepoEntry
  res: OpResult
}

/** Runs `op` on every repo at once, then refreshes each of them (also the ones that failed: they may have half-applied). */
export async function runOnRepos(entries: RepoEntry[], op: (entry: RepoEntry) => Promise<OpResult>): Promise<RepoOpResult[]> {
  return Promise.all(
    entries.map(async (entry) => {
      const res = await entry.repo.run(() => op(entry))
      return { entry, res }
    }),
  )
}

/** One outcome for the lot: ok when every repo was, else "pulled 2/3 · web: Not possible to fast-forward". */
export function summarizeResults(verb: string, results: RepoOpResult[]): OpResult {
  const bad = results.filter((r) => !r.res.ok)
  if (!bad.length) return { ok: true, message: results.length === 1 ? verb : `${verb} ${results.length} repos` }
  const first = bad[0]!
  return { ok: false, message: `${verb} ${results.length - bad.length}/${results.length} · ${first.entry.name}: ${first.res.message}` }
}

/** Branches the repos are on, most common first (detached HEADs and repos not read yet are left out). */
export function branchSpread(entries: RepoEntry[]): { name: string; count: number }[] {
  const count = new Map<string, number>()
  for (const { repo } of entries) {
    const b = repo.status?.branch
    if (b) count.set(b, (count.get(b) ?? 0) + 1)
  }
  return [...count].map(([name, count]) => ({ name, count })).sort((a, b) => b.count - a.count || a.name.localeCompare(b.name))
}
