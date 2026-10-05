import { existsSync, readFileSync } from "node:fs"
import { basename, dirname, isAbsolute, join, resolve } from "node:path"
import YAML from "yaml"
import { COMPOSE_FILENAMES, parseComposeFile } from "./compose.ts"
import { interpolate, parseDotEnv } from "./interpolate.ts"
import { missingEnvFiles, parseEnvFileRefs, type EnvFileRef } from "./envFiles.ts"

export { interpolate, parseDotEnv }
import {
  asEnv,
  asRecord,
  asString,
  asStringList,
  ConfigError,
  hostPortOf,
  parseDuration,
  type HealthCheck,
  type Hook,
  type Hooks,
  type OrbitConfig,
  type RestartPolicy,
  type ServiceConfig,
  type ServiceType,
  type WatchConfig,
} from "./schema.ts"
import { validateGraph } from "../core/graph.ts"

export const CONFIG_FILENAMES = ["orbit.yaml", "orbit.yml"]

/** Walks up from 'start' looking for orbit.yaml. */
export function findConfigFile(start: string): string | undefined {
  let dir = resolve(start)
  while (true) {
    for (const name of CONFIG_FILENAMES) {
      const candidate = join(dir, name)
      if (existsSync(candidate)) return candidate
    }
    const parent = dirname(dir)
    if (parent === dir) return undefined
    dir = parent
  }
}

export function findComposeFile(dir: string): string | undefined {
  return COMPOSE_FILENAMES.map((n) => join(dir, n)).find((f) => existsSync(f))
}

function parseHealth(raw: unknown, path: string, port?: number): HealthCheck | undefined {
  if (raw === false) return undefined
  if (raw === undefined || raw === null) {
    return port ? { tcp: String(port), interval: 1000, timeout: 1000 } : undefined
  }
  if (typeof raw === "string") {
    // shorthand: "http://..." | "tcp:5432" | "cmd: ..." | "container"
    if (/^https?:\/\//.test(raw)) return { http: raw, interval: 2000, timeout: 2000 }
    if (raw === "container") return { container: true, interval: 2000, timeout: 5000 }
    if (raw.startsWith("tcp:")) return { tcp: raw.slice(4), interval: 1000, timeout: 1000 }
    return { cmd: raw, interval: 2000, timeout: 5000 }
  }
  const rec = asRecord(raw, path)
  const health: HealthCheck = {
    http: asString(rec.http, `${path}.http`),
    tcp: asString(rec.tcp, `${path}.tcp`),
    cmd: asString(rec.cmd, `${path}.cmd`),
    container: rec.container === true,
    interval: parseDuration(rec.interval, `${path}.interval`, 2000),
    timeout: parseDuration(rec.timeout, `${path}.timeout`, 2000),
  }
  if (!health.http && !health.tcp && !health.cmd && !health.container) {
    throw new ConfigError("health needs one of http, tcp, cmd or container", path)
  }
  return health
}

/** `watch: ["src/**"]` or `watch: { paths, ignore, debounce, cooldown }` */
function parseWatch(raw: unknown, path: string): WatchConfig | undefined {
  if (raw === undefined || raw === null || raw === false) return undefined
  const isMapping = typeof raw === "object" && !Array.isArray(raw)
  const rec = isMapping ? asRecord(raw, path) : { paths: raw }
  const paths = asStringList(rec.paths, `${path}.paths`)
  if (!paths.length) throw new ConfigError("watch needs at least one path pattern", path)
  return {
    paths,
    ignore: asStringList(rec.ignore, `${path}.ignore`),
    debounce: parseDuration(rec.debounce, `${path}.debounce`, 1000),
    cooldown: parseDuration(rec.cooldown, `${path}.cooldown`, 10_000),
  }
}

/** `"cmd"`, `{ cmd, timeout }`, or a list of either */
function parseHookList(raw: unknown, path: string): Hook[] {
  if (raw === undefined || raw === null || raw === false) return []
  const items = Array.isArray(raw) ? raw : [raw]
  return items.map((item, i) => {
    const at = Array.isArray(raw) ? `${path}[${i}]` : path
    const isMapping = typeof item === "object" && item !== null && !Array.isArray(item)
    const rec: Record<string, unknown> = isMapping ? asRecord(item, at) : { cmd: item }
    const cmd = asString(rec.cmd, `${at}.cmd`)?.trim()
    if (!cmd) throw new ConfigError("a hook needs a non-empty `cmd`", at)
    return { cmd, timeout: parseDuration(rec.timeout, `${at}.timeout`, 60_000) }
  })
}

/** A phase not mentioned in the yaml is inherited from the compose import; none at all gives undefined. */
function parseHooks(rec: Record<string, unknown>, path: string, base?: Hooks): Hooks | undefined {
  const phase = (key: string, field: keyof Hooks) =>
    rec[key] !== undefined ? parseHookList(rec[key], `${path}.${key}`) : (base?.[field] ?? [])
  const hooks: Hooks = {
    preStart: phase("pre_start", "preStart"),
    postStart: phase("post_start", "postStart"),
    postStop: phase("post_stop", "postStop"),
  }
  return hooks.preStart.length || hooks.postStart.length || hooks.postStop.length ? hooks : undefined
}

const RESTART: RestartPolicy[] = ["no", "on-failure", "always"]
const TYPES: ServiceType[] = ["process", "docker", "compose"]

function parseService(
  name: string,
  raw: unknown,
  root: string,
  base: ServiceConfig | undefined,
  composeFile: string | undefined,
): ServiceConfig {
  const path = `services.${name}`
  const rec = typeof raw === "string" ? { cmd: raw } : asRecord(raw, path)

  const explicitType = asString(rec.type, `${path}.type`) as ServiceType | undefined
  if (explicitType && !TYPES.includes(explicitType)) {
    throw new ConfigError(`unknown type "${explicitType}" (expected ${TYPES.join(", ")})`, `${path}.type`)
  }
  const type: ServiceType =
    explicitType ?? (rec.image ? "docker" : rec.service || base?.type === "compose" ? "compose" : "process")

  const cwdRaw = asString(rec.cwd, `${path}.cwd`)
  const cwd = cwdRaw ? (isAbsolute(cwdRaw) ? cwdRaw : resolve(root, cwdRaw)) : (base?.cwd ?? root)
  const ports = rec.ports !== undefined ? asStringList(rec.ports, `${path}.ports`) : (base?.ports ?? [])
  const portRaw = rec.port !== undefined ? Number(rec.port) : undefined
  if (portRaw !== undefined && (!Number.isInteger(portRaw) || portRaw <= 0)) {
    throw new ConfigError(`invalid port ${JSON.stringify(rec.port)}`, `${path}.port`)
  }
  const port = portRaw ?? base?.port ?? ports.map(hostPortOf).find((p) => p !== undefined)

  const restart = (asString(rec.restart, `${path}.restart`) ?? base?.restart ?? "no") as RestartPolicy
  if (!RESTART.includes(restart)) {
    throw new ConfigError(`invalid restart "${restart}" (expected ${RESTART.join(", ")})`, `${path}.restart`)
  }

  const hooks = parseHooks(rec, path, base?.hooks)
  if (rec.oneshot === true && hooks?.postStart.length) {
    throw new ConfigError("a oneshot has no running phase: use post_stop instead", `${path}.post_start`)
  }

  const svc: ServiceConfig = {
    name,
    type,
    description: asString(rec.description, `${path}.description`) ?? base?.description,
    cmd: asString(rec.cmd ?? rec.command, `${path}.cmd`) ?? base?.cmd,
    cwd,
    env: { ...base?.env, ...asEnv(rec.env ?? rec.environment, `${path}.env`) },
    envFiles: parseEnvFileRefs(rec.env_file, `${path}.env_file`, root),
    dependsOn: [
      ...new Set([...(base?.dependsOn ?? []), ...asStringList(rec.depends_on ?? rec.dependsOn, `${path}.depends_on`)]),
    ],
    port,
    url: asString(rec.url, `${path}.url`) ?? base?.url,
    health:
      rec.oneshot === true
        ? undefined
        : rec.health !== undefined || !base
          ? parseHealth(rec.health, `${path}.health`, port)
          : base.health,
    oneshot: rec.oneshot === true || undefined,
    watch: rec.watch !== undefined ? parseWatch(rec.watch, `${path}.watch`) : base?.watch,
    hooks,
    restart,
    startTimeout: parseDuration(rec.start_timeout, `${path}.start_timeout`, base?.startTimeout ?? 60_000),
    stopTimeout: parseDuration(rec.stop_timeout, `${path}.stop_timeout`, base?.stopTimeout ?? 8_000),
    autostart: rec.autostart === undefined ? (base?.autostart ?? true) : rec.autostart !== false,
    image: asString(rec.image, `${path}.image`) ?? base?.image,
    ports,
    volumes: asStringList(rec.volumes, `${path}.volumes`).map((v) => {
      // resolve relative host paths of bind mounts against the project root
      const [host, ...rest] = v.split(":")
      return host && (host.startsWith("./") || host.startsWith("../")) ? [resolve(root, host), ...rest].join(":") : v
    }),
    dockerArgs: asStringList(rec.docker_args, `${path}.docker_args`),
    composeFile: base?.composeFile ?? composeFile,
    composeProject: base?.composeProject,
    composeService: asString(rec.service, `${path}.service`) ?? base?.composeService ?? name,
  }

  if (svc.type === "process" && !svc.cmd) throw new ConfigError("a process service needs `cmd`", path)
  if (svc.type === "docker" && !svc.image) throw new ConfigError("a docker service needs `image`", path)
  if (svc.type === "compose" && svc.envFiles.length) {
    throw new ConfigError("env_file has no effect on compose services: set it in the compose file", `${path}.env_file`)
  }
  if (svc.type === "compose" && !svc.composeFile) {
    throw new ConfigError("a compose service needs a compose file (set top-level `compose:`)", path)
  }
  return svc
}

export interface LoadOptions {
  /** directory to start searching from (default: cwd) */
  dir?: string
  /** explicit config file */
  file?: string
  /** a directory with neither orbit.yaml nor compose is a project with no services instead of an error */
  allowEmpty?: boolean
  env?: Record<string, string | undefined>
}

export function loadConfig(opts: LoadOptions = {}): OrbitConfig {
  const startDir = resolve(opts.dir ?? process.cwd())
  const file = opts.file ? resolve(opts.file) : findConfigFile(startDir)

  if (!file) {
    // No orbit.yaml: fall back to a plain docker-compose project.
    const compose = findComposeFile(startDir)
    if (!compose) {
      if (opts.allowEmpty) return { name: basename(startDir) || "orbit", root: startDir, services: {}, groups: {} }
      throw new ConfigError(`no orbit.yaml or docker-compose.yml found in ${startDir} (run \`orbit init\`)`)
    }
    const services = parseComposeFile(compose, readFileSync(compose, "utf8"))
    const config: OrbitConfig = {
      name: services[0]?.composeProject ?? "orbit",
      root: startDir,
      services: Object.fromEntries(services.map((s) => [s.name, s])),
      groups: {},
    }
    validateGraph(config)
    return config
  }

  if (!existsSync(file)) throw new ConfigError(`config file not found: ${file}`)
  const root = dirname(file)
  const dotEnvPath = join(root, ".env")
  const dotEnv = existsSync(dotEnvPath) ? parseDotEnv(readFileSync(dotEnvPath, "utf8")) : {}
  const vars = { ...dotEnv, ...(opts.env ?? process.env) }

  let parsed: unknown
  try {
    parsed = YAML.parse(readFileSync(file, "utf8"))
  } catch (err) {
    const message = (err as Error).message
    // an unquoted glob like **/*.py starts with `*`, which YAML reads as an alias
    const hint = /alias/i.test(message) ? ` (quote glob patterns that start with *, e.g. "**/*.py")` : ""
    throw new ConfigError(`invalid YAML: ${message}${hint}`, file)
  }
  const doc = interpolate(asRecord(parsed, file), vars)
  const globalEnv = asEnv(doc.env, "env")
  const globalEnvFiles = parseEnvFileRefs(doc.env_file, "env_file", root)

  // compose: <file> | [files] | false. When omitted, a compose file next to orbit.yaml is imported.
  const composeFiles =
    doc.compose === false
      ? []
      : doc.compose === undefined
        ? [findComposeFile(root)].filter((f): f is string => !!f)
        : asStringList(doc.compose, "compose").map((f) => resolve(root, f))

  const imported = new Map<string, ServiceConfig>()
  for (const cf of composeFiles) {
    if (!existsSync(cf)) throw new ConfigError(`compose file not found: ${cf}`, "compose")
    for (const svc of parseComposeFile(cf, readFileSync(cf, "utf8"), asString(doc.compose_project, "compose_project"), vars)) {
      imported.set(svc.name, svc)
    }
  }

  const services: Record<string, ServiceConfig> = {}
  const rawServices = asRecord(doc.services, "services")
  for (const [name, svc] of imported) {
    if (!(name in rawServices)) services[name] = svc
  }
  for (const [name, raw] of Object.entries(rawServices)) {
    services[name] = parseService(name, raw ?? {}, root, imported.get(name), composeFiles[0])
  }
  for (const svc of Object.values(services)) {
    svc.env = { ...globalEnv, ...svc.env }
    // compose services get their environment from docker compose itself
    if (svc.type !== "compose") svc.envFiles = [...globalEnvFiles, ...svc.envFiles]
    const missing = missingEnvFiles(svc.envFiles)
    if (missing.length) {
      throw new ConfigError(`env_file not found: ${missing.join(", ")} (use { path, required: false } for optional files)`, `services.${svc.name}.env_file`)
    }
  }

  const groups: Record<string, string[]> = {}
  for (const [g, members] of Object.entries(asRecord(doc.groups, "groups"))) {
    groups[g] = asStringList(members, `groups.${g}`)
    for (const m of groups[g]!) {
      if (!services[m]) throw new ConfigError(`unknown service "${m}"`, `groups.${g}`)
    }
  }

  const config: OrbitConfig = {
    name: asString(doc.name, "name") ?? dirname(file).split("/").pop() ?? "orbit",
    root,
    file,
    services,
    groups,
  }
  validateGraph(config)
  return config
}
