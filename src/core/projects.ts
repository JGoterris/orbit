import { existsSync, readdirSync, readFileSync } from "node:fs"
import { homedir } from "node:os"
import { dirname, join, resolve } from "node:path"
import type { OrbitConfig } from "../config/schema.ts"
import { configDir, writeJsonAtomic } from "./userConfig.ts"
import { pidAlive, procStartTime } from "./platform/index.ts"
import { readLock, readState, stateDir } from "./state.ts"

/** A project orbit has opened before. Kept in ~/.config/orbit/projects.json, shared by every project. */
export interface ProjectEntry {
  /** absolute directory (the folder holding orbit.yaml / docker-compose.yml, or any folder) */
  path: string
  name: string
  /** epoch ms of the last time it was opened */
  lastOpened: number
  pinned?: boolean
}

/** Unpinned entries beyond this many (oldest first) are dropped on write. */
const MAX_RECENT = 30

const file = () => join(configDir(), "projects.json")

export function readProjects(): ProjectEntry[] {
  try {
    const raw = JSON.parse(readFileSync(file(), "utf8")) as { projects?: unknown }
    if (!Array.isArray(raw.projects)) return []
    return raw.projects.flatMap((p): ProjectEntry[] => {
      if (typeof p !== "object" || p === null) return []
      const { path, name, lastOpened, pinned } = p as Record<string, unknown>
      if (typeof path !== "string" || !path || typeof name !== "string") return []
      return [{ path, name, lastOpened: typeof lastOpened === "number" ? lastOpened : 0, ...(pinned === true ? { pinned: true } : {}) }]
    })
  } catch {
    return []
  }
}

function writeProjects(list: ProjectEntry[]): boolean {
  const recent = sortProjects(list.filter((p) => !p.pinned)).slice(0, MAX_RECENT)
  return writeJsonAtomic(file(), { projects: [...list.filter((p) => p.pinned), ...recent] })
}

/** Pinned first, then most recently opened. */
export function sortProjects(list: ProjectEntry[]): ProjectEntry[] {
  return [...list].sort((a, b) => Number(!!b.pinned) - Number(!!a.pinned) || b.lastOpened - a.lastOpened)
}

/** Records that `path` was just opened (keeping its pin). Returns false if the registry could not be written. */
export function registerProject(path: string, name: string, now = Date.now()): boolean {
  const list = readProjects()
  const prev = list.find((p) => p.path === path)
  const entry: ProjectEntry = { path, name, lastOpened: now, ...(prev?.pinned ? { pinned: true } : {}) }
  return writeProjects([...list.filter((p) => p.path !== path), entry])
}

export function setPinned(path: string, pinned: boolean): boolean {
  return writeProjects(readProjects().map((p) => (p.path === path ? { ...p, pinned: pinned || undefined } : p)))
}

export function forgetProject(path: string): boolean {
  return writeProjects(readProjects().filter((p) => p.path !== path))
}

export interface ProjectStatus {
  /** the folder is still there */
  exists: boolean
  /** pid of another orbit that has the project open right now */
  openIn?: number
  /** processes orbit left running there (containers are not tracked) */
  running: number
}

export function projectStatus(p: ProjectEntry): ProjectStatus {
  const dir = stateDir({ name: p.name, root: p.path } as OrbitConfig)
  const holder = readLock(dir)
  const running = Object.values(readState(dir).services).filter(
    (s) => s.pid !== undefined && pidAlive(s.pid) && (s.startTime === undefined || procStartTime(s.pid) === s.startTime),
  ).length
  return { exists: existsSync(p.path), openIn: holder && holder !== process.pid ? holder : undefined, running }
}

/** `~` and relative paths to an absolute path. */
export function expandPath(input: string, cwd = process.cwd()): string {
  const t = input.trim()
  if (t === "~") return homedir()
  if (t.startsWith("~/")) return join(homedir(), t.slice(2))
  return resolve(cwd, t)
}

/** Whether what was typed in the project picker is a path rather than a search. */
export const looksLikePath = (input: string) => /^(\/|~|\.{1,2}(\/|$))/.test(input.trim())

/**
 * Shell-style tab completion of a directory path: a single match is completed (with a trailing `/`),
 * several are completed up to their common prefix, none leaves the input as is.
 */
export function completePath(input: string, cwd = process.cwd()): string {
  const home = homedir()
  const full = expandPath(input, cwd)
  const endsInSlash = input.endsWith("/")
  const parent = endsInSlash || input === "~" ? full : dirname(full)
  const prefix = endsInSlash || input === "~" ? "" : full.slice(parent.length + (parent.endsWith("/") ? 0 : 1))
  let names: string[]
  try {
    names = readdirSync(parent, { withFileTypes: true })
      .filter((e) => (e.isDirectory() || e.isSymbolicLink()) && e.name.startsWith(prefix) && (prefix.startsWith(".") || !e.name.startsWith(".")))
      .map((e) => e.name)
      .sort()
  } catch {
    return input
  }
  if (!names.length) return input
  let common = names[0]!
  for (const n of names) while (!n.startsWith(common)) common = common.slice(0, -1)
  if (names.length > 1 && common === prefix) return input // several candidates, nothing more in common
  const done = join(parent, common) + (names.length === 1 ? "/" : "")
  // keep the way the user wrote it (~ stays ~, relative stays relative)
  if (input.startsWith("~")) return done.startsWith(home) ? `~${done.slice(home.length)}` : done
  if (!input.startsWith("/")) return done.startsWith(cwd + "/") ? `${input.startsWith("./") ? "./" : ""}${done.slice(cwd.length + 1)}` : done
  return done
}

/**
 * Resolves what `orbit open <x>` was given: a registered project by exact name, then by path, then by a
 * name fragment. Returns the entry, or an error message when nothing (or more than one project) matches.
 */
export function findProject(query: string, list: ProjectEntry[] = readProjects()): ProjectEntry | string {
  const q = query.trim()
  const byName = list.filter((p) => p.name === q)
  if (byName.length === 1) return byName[0]!
  if (byName.length > 1) return `several projects are called "${q}": ${byName.map((p) => p.path).join(", ")} (use the path)`
  const path = expandPath(q)
  const byPath = list.find((p) => p.path === path)
  if (byPath) return byPath
  const fuzzy = list.filter((p) => p.name.toLowerCase().includes(q.toLowerCase()))
  if (fuzzy.length === 1) return fuzzy[0]!
  if (fuzzy.length > 1) return `"${q}" matches ${fuzzy.map((p) => p.name).join(", ")}`
  return `no project "${q}" (see \`orbit projects\`)`
}
