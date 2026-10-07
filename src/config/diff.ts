import type { OrbitConfig, ServiceConfig } from "./schema.ts"

export interface ServiceChange {
  name: string
  /** keys of the service config that differ */
  fields: string[]
  /** a running instance has to be restarted to pick the change up */
  restart: boolean
}

export interface ConfigDiff {
  added: string[]
  removed: string[]
  changed: ServiceChange[]
  /** the `groups:` section differs (applied without restarting anything) */
  groups: boolean
  /** the project name is part of the state dir and the socket: it cannot change while orbit runs */
  nameChanged?: { from: string; to: string }
}

/** Changes that take effect on a running service without restarting it. */
const HOT: ReadonlySet<string> = new Set([
  "description", "url", "console", "autostart", "restart", "memLimit", "leakDetection",
  "startTimeout", "stopTimeout", "health", "watch",
])

/** JSON with sorted keys, so two equal values compare equal whatever the order they were built in. */
function stable(value: unknown): string {
  return JSON.stringify(value, (_, v) =>
    v && typeof v === "object" && !Array.isArray(v)
      ? Object.fromEntries(Object.entries(v).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)))
      : v,
  )
}

function changedFields(a: ServiceConfig, b: ServiceConfig): string[] {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)])
  return [...keys].filter((k) => stable((a as unknown as Record<string, unknown>)[k]) !== stable((b as unknown as Record<string, unknown>)[k]))
}

export function diffConfig(prev: OrbitConfig, next: OrbitConfig): ConfigDiff {
  const added = Object.keys(next.services).filter((n) => !prev.services[n])
  const removed = Object.keys(prev.services).filter((n) => !next.services[n])
  const changed: ServiceChange[] = []
  for (const [name, svc] of Object.entries(next.services)) {
    const before = prev.services[name]
    if (!before) continue
    const fields = changedFields(before, svc)
    if (fields.length) changed.push({ name, fields, restart: fields.some((f) => !HOT.has(f)) })
  }
  return {
    added,
    removed,
    changed,
    groups: stable(prev.groups) !== stable(next.groups),
    nameChanged: prev.name !== next.name ? { from: prev.name, to: next.name } : undefined,
  }
}

export function isEmptyDiff(d: ConfigDiff): boolean {
  return !d.added.length && !d.removed.length && !d.changed.length && !d.groups && !d.nameChanged
}

/** Services a running instance of `prev` has to restart for `d` (the changed ones that need it). */
export function restartNames(d: ConfigDiff): string[] {
  return d.changed.filter((c) => c.restart).map((c) => c.name)
}

/** One line per difference, compose-style: `+ api`, `- worker`, `~ web (cmd, env) ↻`. */
export function describeDiff(d: ConfigDiff): string[] {
  return [
    ...d.added.map((n) => `+ ${n}`),
    ...d.removed.map((n) => `- ${n}`),
    ...d.changed.map((c) => `~ ${c.name} (${c.fields.join(", ")})${c.restart ? " ↻" : ""}`),
    ...(d.groups ? ["~ groups"] : []),
    ...(d.nameChanged ? [`! name ${d.nameChanged.from} → ${d.nameChanged.to}: not applied while running (\`orbit down\` and reopen)`] : []),
  ]
}
