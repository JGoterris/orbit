import { existsSync, readFileSync } from "node:fs"
import { isAbsolute, relative, resolve } from "node:path"
import { parseDotEnv } from "./interpolate.ts"
import { asRecord, asString, ConfigError } from "./schema.ts"

export interface EnvFileRef {
  path: string
  /** a missing required file is an error; optional ones are skipped */
  required: boolean
}

/** `env_file: .env` | `[.env, .env.local]` | `[{ path: .env.local, required: false }]` */
export function parseEnvFileRefs(raw: unknown, path: string, root: string): EnvFileRef[] {
  if (raw === undefined || raw === null) return []
  const items = Array.isArray(raw) ? raw : [raw]
  return items.map((item, i) => {
    const p = Array.isArray(raw) ? `${path}[${i}]` : path
    if (typeof item === "string") return { path: resolvePath(item, root), required: true }
    const rec = asRecord(item, p)
    const file = asString(rec.path, `${p}.path`)
    if (!file) throw new ConfigError("env_file entry needs `path`", p)
    return { path: resolvePath(file, root), required: rec.required !== false }
  })
}

function resolvePath(file: string, root: string) {
  return isAbsolute(file) ? file : resolve(root, file)
}

export function missingEnvFiles(refs: readonly EnvFileRef[]): string[] {
  return refs.filter((r) => r.required && !existsSync(r.path)).map((r) => r.path)
}

/** Reads the files in order; later files override earlier ones. Throws if a required file is missing. */
export function readEnvFiles(refs: readonly EnvFileRef[]): Record<string, string> {
  const missing = missingEnvFiles(refs)
  if (missing.length) throw new Error(`env_file not found: ${missing.join(", ")}`)
  const out: Record<string, string> = {}
  for (const ref of refs) {
    if (!existsSync(ref.path)) continue
    Object.assign(out, parseDotEnv(readFileSync(ref.path, "utf8")))
  }
  return out
}

export interface EnvEntry {
  key: string
  value: string
  /** the .env file it came from (relative to root), or "inline" */
  source: string
}

/** Same merge as a service start (inline over files), but never throws: missing required files are reported. */
export function resolveEnv(
  svc: { env: Record<string, string>; envFiles: readonly EnvFileRef[] },
  root: string,
): { entries: EnvEntry[]; missing: string[] } {
  const merged = new Map<string, EnvEntry>()
  for (const ref of svc.envFiles) {
    if (!existsSync(ref.path)) continue
    const source = relative(root, ref.path) || ref.path
    for (const [key, value] of Object.entries(parseDotEnv(readFileSync(ref.path, "utf8")))) merged.set(key, { key, value, source })
  }
  for (const [key, value] of Object.entries(svc.env)) merged.set(key, { key, value, source: "inline" })
  return {
    entries: [...merged.values()].sort((a, b) => a.key.localeCompare(b.key)),
    missing: missingEnvFiles(svc.envFiles).map((f) => relative(root, f) || f),
  }
}
