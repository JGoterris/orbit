import { watch as fsWatch, existsSync, statSync, type FSWatcher } from "node:fs"
import { join, sep } from "node:path"
import type { WatchConfig } from "../config/schema.ts"

/** Never worth restarting for: VCS internals, dependencies and editor scratch files. */
export const DEFAULT_IGNORE = ["**/.git/**", "**/node_modules/**", "**/*.swp", "**/*.swx", "**/*~", "**/.#*", "**/4913"]

const GLOB_CHARS = /[*?[\]{}!]/

/**
 * Directories to register watchers on: the static prefix of each pattern, so `src/**` watches `src/`
 * instead of the whole cwd. A pattern with no directory part (`package.json`) watches the cwd flat.
 */
export function watchRoots(patterns: string[]): Array<{ dir: string; recursive: boolean }> {
  const roots = new Map<string, boolean>()
  for (const pattern of patterns) {
    const segments = pattern.replace(/^\.\//, "").split("/")
    let n = 0
    while (n < segments.length - 1 && !GLOB_CHARS.test(segments[n]!)) n++
    // anything left beyond a single path segment (or a trailing `**`) may live in a subdirectory
    const recursive = segments.length - n > 1 || segments[segments.length - 1]!.includes("**")
    const dir = segments.slice(0, n).join("/")
    roots.set(dir, (roots.get(dir) ?? false) || recursive)
  }
  // a recursive root already covers the roots below it
  const list = [...roots].map(([dir, recursive]) => ({ dir, recursive }))
  return list.filter((r) => !list.some((o) => o !== r && o.recursive && (o.dir === "" || r.dir.startsWith(`${o.dir}/`))))
}

export interface FileWatcherHooks {
  /** debounce and cooldown elapsed: these files changed since the last trigger */
  onTrigger: (files: string[]) => void
  onError: (message: string) => void
  /** changes are queued (waiting for the debounce / cooldown), or the queue emptied */
  onPending?: (pending: boolean) => void
}

/**
 * Watches a service's files and batches changes: it fires once the files have been quiet for `debounce`,
 * and never twice within `cooldown`. Changes arriving meanwhile join the same batch.
 */
export class FileWatcher {
  private watchers: FSWatcher[] = []
  private batch = new Set<string>()
  private timer?: ReturnType<typeof setTimeout>
  private lastFire = 0
  private match: Bun.Glob[]
  private ignore: Bun.Glob[]
  private _paused = false

  constructor(
    private cwd: string,
    private cfg: WatchConfig,
    private hooks: FileWatcherHooks,
  ) {
    this.match = cfg.paths.map((p) => new Bun.Glob(p.replace(/^\.\//, "")))
    this.ignore = [...DEFAULT_IGNORE, ...cfg.ignore].map((p) => new Bun.Glob(p))
  }

  get paused() {
    return this._paused
  }

  get pending() {
    return this.batch.size > 0
  }

  set paused(value: boolean) {
    if (value === this._paused) return
    this._paused = value
    if (value) this.clear()
  }

  start() {
    for (const { dir, recursive } of watchRoots(this.cfg.paths)) {
      const abs = dir ? join(this.cwd, dir) : this.cwd
      if (!existsSync(abs)) {
        this.hooks.onError(`watch: ${dir || "."} does not exist, not watching it`)
        continue
      }
      try {
        const w = fsWatch(abs, { recursive }, (_event, filename) => {
          if (filename) this.touch(dir ? join(dir, String(filename)) : String(filename))
        })
        w.on("error", (err) => this.fail(err))
        this.watchers.push(w)
      } catch (err) {
        this.fail(err)
      }
    }
  }

  close() {
    this.clear()
    for (const w of this.watchers.splice(0)) w.close()
  }

  /** Registers a changed file (relative to cwd) if it matches `paths` and not `ignore`. Exposed for tests. */
  touch(file: string) {
    if (this._paused) return
    const rel = sep === "/" ? file : file.split(sep).join("/")
    if (!this.match.some((g) => g.match(rel)) || this.ignore.some((g) => g.match(rel))) return
    // macOS and Windows also report the folders that contain a changed file: only files are changes
    try {
      if (statSync(join(this.cwd, rel)).isDirectory()) return
    } catch {}
    const wasEmpty = this.batch.size === 0
    this.batch.add(rel)
    if (wasEmpty) this.hooks.onPending?.(true)
    this.schedule(this.cfg.debounce)
  }

  private schedule(delay: number) {
    clearTimeout(this.timer)
    this.timer = setTimeout(() => this.fire(), delay)
  }

  private fire() {
    const sinceLast = Date.now() - this.lastFire
    if (sinceLast < this.cfg.cooldown) return this.schedule(this.cfg.cooldown - sinceLast)
    const files = [...this.batch]
    this.batch.clear()
    this.lastFire = Date.now()
    this.hooks.onPending?.(false)
    if (files.length) this.hooks.onTrigger(files)
  }

  private clear() {
    clearTimeout(this.timer)
    if (this.batch.size) {
      this.batch.clear()
      this.hooks.onPending?.(false)
    }
  }

  private fail(err: unknown) {
    const e = err as NodeJS.ErrnoException
    const hint = e.code === "ENOSPC" ? " (raise fs.inotify.max_user_watches)" : ""
    this.hooks.onError(`watch: ${e.message}${hint}`)
  }
}
