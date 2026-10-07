import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import YAML from "yaml"
import schema from "../../orbit.schema.json"
import { findConfigFile, loadConfig, type LoadOptions } from "./load.ts"
import { ConfigError, type OrbitConfig } from "./schema.ts"

interface Schema {
  $ref?: string
  type?: string | string[]
  properties?: Record<string, Schema>
  additionalProperties?: Schema | boolean
  items?: Schema
  oneOf?: Schema[]
  anyOf?: Schema[]
}

export interface UnknownKey {
  /** dotted path of the mapping that holds the key, e.g. `services.api` */
  path: string
  key: string
  suggestion?: string
}

const isMapping = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v)

function resolveRef(s: Schema, root: unknown): Schema {
  if (!s.$ref) return s
  let node: any = root
  for (const part of s.$ref.replace(/^#\//, "").split("/")) node = node?.[part]
  if (!node) throw new Error(`unresolved $ref ${s.$ref}`)
  return resolveRef(node, root)
}

function distance(a: string, b: string): number {
  const row = Array.from({ length: b.length + 1 }, (_, j) => j)
  for (let i = 1; i <= a.length; i++) {
    let prev = row[0]!
    row[0] = i
    for (let j = 1; j <= b.length; j++) {
      const tmp = row[j]!
      row[j] = Math.min(row[j]! + 1, row[j - 1]! + 1, prev + (a[i - 1] === b[j - 1] ? 0 : 1))
      prev = tmp
    }
  }
  return row[b.length]!
}

function suggest(key: string, known: string[]): string | undefined {
  let best: string | undefined
  let bestD = 3
  for (const k of known) {
    const d = distance(key.toLowerCase(), k.toLowerCase())
    if (d < bestD) [best, bestD] = [k, d]
  }
  return best
}

/** Keys of `doc` that the schema does not allow (only where it says `additionalProperties: false`). */
export function unknownKeys(doc: unknown, root: Schema = schema as Schema): UnknownKey[] {
  const out: UnknownKey[] = []
  const walk = (value: unknown, s: Schema, path: string) => {
    s = resolveRef(s, root)
    const branches = [...(s.oneOf ?? []), ...(s.anyOf ?? [])].map((b) => resolveRef(b, root))
    if (branches.length) {
      // follow the branch that describes this kind of value (mapping or list); scalars have no keys to check
      const kind = Array.isArray(value) ? "array" : isMapping(value) ? "object" : undefined
      if (kind) for (const b of branches) if (b.type === kind) walk(value, b, path)
      return
    }
    if (Array.isArray(value)) {
      if (s.items) value.forEach((v, i) => walk(v, s.items!, `${path}[${i}]`))
      return
    }
    if (!isMapping(value)) return
    const props = s.properties ?? {}
    for (const [key, child] of Object.entries(value)) {
      const at = path ? `${path}.${key}` : key
      if (props[key]) walk(child, props[key]!, at)
      else if (isMapping(s.additionalProperties)) walk(child, s.additionalProperties as Schema, at)
      else if (s.additionalProperties === false) out.push({ path, key, suggestion: suggest(key, Object.keys(props)) })
    }
  }
  walk(doc, root, "")
  return out
}

export interface Validation {
  file?: string
  config?: OrbitConfig
  errors: string[]
}

/** The real loader decides what is valid; on top of that, keys it would silently ignore are reported. */
export function validateFile(opts: LoadOptions = {}): Validation {
  const errors: string[] = []
  let config: OrbitConfig | undefined
  try {
    config = loadConfig(opts)
  } catch (err) {
    if (!(err instanceof ConfigError)) throw err
    errors.push(err.message)
  }
  const file = config?.file ?? (opts.file ? resolve(opts.file) : findConfigFile(resolve(opts.dir ?? process.cwd())))
  if (file) {
    let doc: unknown
    try {
      doc = YAML.parse(readFileSync(file, "utf8"))
    } catch {
      // the loader already reported invalid YAML
    }
    for (const u of unknownKeys(doc)) {
      const at = u.path ? `${u.path}.${u.key}` : u.key
      errors.push(`${at}: unknown key${u.suggestion ? ` (did you mean "${u.suggestion}"?)` : ""}`)
    }
  }
  return { file, config, errors }
}
