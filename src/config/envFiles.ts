import { existsSync, readFileSync } from "node:fs"
import { isAbsolute, resolve } from "node:path"
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
