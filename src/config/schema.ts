import { parseMemSize } from "../core/metrics.ts"
import type { EnvFileRef } from "./envFiles.ts"

/** `external`: something orbit does not run (a SaaS API, a managed database): it is only health-checked */
export type ServiceType = "process" | "docker" | "compose" | "external"
export type RestartPolicy = "no" | "on-failure" | "always"

export interface HealthCheck {
  /** HTTP URL that must answer with a status < 500 */
  http?: string
  /** host:port (or just port) that must accept TCP connections */
  tcp?: string
  /** shell command that must exit 0 */
  cmd?: string
  /** use the container's own Docker HEALTHCHECK status */
  container?: boolean
  interval: number
  timeout: number
}

/** Extra readiness condition read from the service's output */
export interface ReadyWhen {
  /** substring, or `/regex/flags`, that a stdout/stderr line must match */
  log: string
}

export interface WatchConfig {
  /** globs relative to the service's cwd; a change to a matching file restarts the service */
  paths: string[]
  ignore: string[]
  /** ms of quiet needed after the last change before restarting */
  debounce: number
  /** minimum ms between two automatic restarts; changes in between are batched */
  cooldown: number
}

export interface Hook {
  cmd: string
  /** ms before the hook is killed and counts as failed */
  timeout: number
}

export interface Hooks {
  preStart: Hook[]
  postStart: Hook[]
  postStop: Hook[]
}

export interface ServiceConfig {
  name: string
  type: ServiceType
  description?: string
  cmd?: string
  /** shell that runs cmd, hooks, health.cmd and console (default: sh on POSIX, cmd.exe on Windows) */
  shell?: string
  /** interactive command for the `i` console (psql, rails console...): process → runs in the service's cwd/env, docker/compose → inside the container */
  console?: string
  cwd: string
  /** inline variables; they override the ones read from envFiles */
  env: Record<string, string>
  /** .env files read (in order) every time the service starts */
  envFiles: EnvFileRef[]
  dependsOn: string[]
  /** main host port, used for the port-in-use check, default health and "open" */
  port?: number
  url?: string
  health?: HealthCheck
  /** the service is not ready until its output matches (and `health`, if any, passes) */
  readyWhen?: ReadyWhen
  /** restart the service automatically when matching files change */
  watch?: WatchConfig
  /** host commands run around the service's life; undefined when it has none */
  hooks?: Hooks
  restart: RestartPolicy
  /** ms to wait for a service to become ready before giving up */
  startTimeout: number
  /** ms to wait after SIGTERM before SIGKILL */
  stopTimeout: number
  autostart: boolean
  /** memory (bytes) above which orbit alerts; orbit does not enforce it (compose/docker do, if they set it) */
  memLimit?: number
  /** warn about sustained memory growth (default on) */
  leakDetection: boolean
  /** a task that runs to completion (build, migration, provisioning): ready once it exits 0 */
  oneshot?: boolean
  // docker
  image?: string
  ports: string[]
  volumes: string[]
  dockerArgs: string[]
  // compose
  composeFile?: string
  composeProject?: string
  composeService?: string
  /** hash of the service's block in the compose file, so a change orbit does not parse (build, command…) still shows up in a config diff */
  composeHash?: string
}

export interface OrbitConfig {
  name: string
  root: string
  file?: string
  services: Record<string, ServiceConfig>
  groups: Record<string, string[]>
}

export class ConfigError extends Error {
  constructor(
    message: string,
    public path?: string,
  ) {
    super(path ? `${path}: ${message}` : message)
    this.name = "ConfigError"
  }
}

const DURATION_RE = /^(\d+(?:\.\d+)?)\s*(ms|s|m|h)?$/

export function parseDuration(value: unknown, path: string, fallback: number): number {
  if (value === undefined || value === null) return fallback
  if (typeof value === "number") return value
  if (typeof value !== "string") throw new ConfigError(`expected a duration like "2s", got ${JSON.stringify(value)}`, path)
  const m = DURATION_RE.exec(value.trim())
  if (!m) throw new ConfigError(`invalid duration ${JSON.stringify(value)} (use e.g. 500ms, 2s, 1m)`, path)
  const n = Number(m[1])
  const unit = m[2] ?? "ms"
  return n * { ms: 1, s: 1000, m: 60_000, h: 3_600_000 }[unit]!
}

/** `"Listening on"` matches as a substring; `"/listening on \\d+/i"` as a regex. Throws on an invalid regex. */
export function compileLogPattern(src: string): (line: string) => boolean {
  const m = /^\/(.+)\/([a-z]*)$/s.exec(src)
  if (!m) return (line) => line.includes(src)
  const re = new RegExp(m[1]!, m[2]!.replace(/[gy]/g, "")) // g / y make `test` stateful
  return (line) => re.test(line)
}

export function asString(value: unknown, path: string): string | undefined {
  if (value === undefined || value === null) return undefined
  if (typeof value === "string") return value
  if (typeof value === "number" || typeof value === "boolean") return String(value)
  throw new ConfigError(`expected a string, got ${typeof value}`, path)
}

export function asStringList(value: unknown, path: string): string[] {
  if (value === undefined || value === null) return []
  if (typeof value === "string") return [value]
  if (!Array.isArray(value)) throw new ConfigError("expected a list", path)
  return value.map((v, i) => asString(v, `${path}[${i}]`)!)
}

export function asRecord(value: unknown, path: string): Record<string, unknown> {
  if (value === undefined || value === null) return {}
  if (typeof value !== "object" || Array.isArray(value)) throw new ConfigError("expected a mapping", path)
  return value as Record<string, unknown>
}

/** Accepts `{ KEY: value }` or `["KEY=value"]` */
export function asEnv(value: unknown, path: string): Record<string, string> {
  if (Array.isArray(value)) {
    const out: Record<string, string> = {}
    value.forEach((entry, i) => {
      const s = asString(entry, `${path}[${i}]`)!
      const eq = s.indexOf("=")
      if (eq === -1) out[s] = process.env[s] ?? ""
      else out[s.slice(0, eq)] = s.slice(eq + 1)
    })
    return out
  }
  const rec = asRecord(value, path)
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(rec)) out[k] = asString(v, `${path}.${k}`) ?? ""
  return out
}

/** `mem_limit` / `deploy.resources.limits.memory`: "1G", "512m", or bytes */
export function asMemSize(value: unknown, path: string): number | undefined {
  if (value === undefined || value === null) return undefined
  if (typeof value !== "string" && typeof value !== "number") throw new ConfigError(`expected a size like "1G", got ${typeof value}`, path)
  const n = parseMemSize(value)
  if (n === undefined) throw new ConfigError(`invalid memory size ${JSON.stringify(value)} (use e.g. 512m, 1G)`, path)
  return n
}

/** "8080:80" -> 8080, "127.0.0.1:5432:5432" -> 5432, "3000" -> 3000 */
export function hostPortOf(mapping: string): number | undefined {
  const withoutProto = mapping.split("/")[0]!
  const parts = withoutProto.split(":")
  const host = parts.length >= 2 ? parts[parts.length - 2] : parts[0]
  const n = Number(host)
  return Number.isInteger(n) && n > 0 ? n : undefined
}
