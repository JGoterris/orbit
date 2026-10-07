import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs"
import { basename, join, relative } from "node:path"
import YAML from "yaml"
import type { OrbitConfig } from "./config/schema.ts"
import { findComposeFile, type LoadOptions } from "./config/load.ts"
import { validateFile } from "./config/validate.ts"
import { parseComposeFile } from "./config/compose.ts"
import { attachDesktopNotifier } from "./core/desktopNotify.ts"
import { exec } from "./core/exec.ts"
import { cleanLine, FileTail, matcher, pipeLines, readTail, type LogLine } from "./core/logs.ts"
import { levels, depMapOf } from "./core/graph.ts"
import { describeHealth } from "./core/health.ts"
import { formatBytes } from "./core/metrics.ts"
import { containerName, ProcessRunner } from "./core/runners.ts"
import { projectStatus, readProjects, sortProjects } from "./core/projects.ts"
import { IpcClient } from "./core/ipc/client.ts"
import { socketPath } from "./core/ipc/endpoint.ts"
import { isServed } from "./core/ipc/daemon.ts"
import { IpcServer } from "./core/ipc/server.ts"
import type { Notification } from "./core/ipc/protocol.ts"
import { procFiles, readLock, readState, stateDir, writeState } from "./core/state.ts"
import { Supervisor, type ServiceState, type Status } from "./core/supervisor.ts"
import { gridToString, layoutGraph, paintGraph } from "./ui/graphLayout.ts"

const useColor = process.stdout.isTTY && !process.env.NO_COLOR
const ansi = (code: string) => (s: string) => (useColor ? `\x1b[${code}m${s}\x1b[0m` : s)
const c = {
  dim: ansi("2"),
  bold: ansi("1"),
  red: ansi("31"),
  green: ansi("32"),
  yellow: ansi("33"),
  blue: ansi("34"),
  magenta: ansi("35"),
  cyan: ansi("36"),
}
const PREFIX_COLORS = [c.blue, c.green, c.yellow, c.magenta, c.cyan]

// ------------------------------------------------------------------ graph / ls

export function runGraph(config: OrbitConfig): number {
  const layout = layoutGraph(depMapOf(config), Object.keys(config.services))
  const grid = paintGraph(layout, {
    text: "",
    defaultEdge: "",
    edgeColor: () => undefined,
    node: (name) => {
      const svc = config.services[name]!
      return {
        icon: "○",
        iconColor: "",
        title: name,
        subtitle: `${svc.type}${svc.port ? ` :${svc.port}` : ""}`,
        subtitleColor: "",
        border: "",
      }
    },
  })
  console.log(gridToString(grid))
  console.log(c.dim("\n──▶ starts before"))
  return 0
}

export function runList(config: OrbitConfig): number {
  const lv = levels(depMapOf(config))
  const rows = Object.values(config.services).map((s) => [
    s.name,
    s.type,
    s.port ? `:${s.port}` : "",
    String(lv[s.name]),
    s.dependsOn.join(", "),
    s.type === "external" ? describeHealth(s.health) : s.type === "process" ? s.cmd! : s.type === "docker" ? s.image! : `${relative(config.root, s.composeFile!)}#${s.composeService}`,
  ])
  const header = ["SERVICE", "TYPE", "PORT", "LEVEL", "DEPENDS ON", "RUNS"]
  const widths = header.map((h, i) => Math.max(h.length, ...rows.map((r) => r[i]!.length)))
  const line = (r: string[]) => r.map((v, i) => v.padEnd(widths[i]!)).join("  ")
  console.log(c.bold(line(header)))
  rows.forEach((r) => console.log(line(r)))
  for (const [g, members] of Object.entries(config.groups)) console.log(c.dim(`group ${g}: ${members.join(", ")}`))
  return 0
}

/** `orbit projects`: the projects orbit remembers (pinned first, then recent) and what is going on in them. */
export function runProjects(): number {
  const list = sortProjects(readProjects())
  if (!list.length) {
    console.log(c.dim("no projects yet: they are remembered the first time orbit opens them"))
    return 0
  }
  const rows = list.map((p) => {
    const st = projectStatus(p)
    const flags = [st.openIn ? c.cyan(`open (pid ${st.openIn})`) : "", st.running ? c.green(`● ${st.running} up`) : "", st.exists ? "" : c.red("missing")].filter(Boolean)
    return { label: `${p.pinned ? "★" : " "} ${p.name}`, path: p.path, flags: flags.join(" ") }
  })
  const w = Math.max(...rows.map((r) => r.label.length))
  for (const r of rows) console.log(`${r.label.padEnd(w)}  ${c.dim(r.path)}  ${r.flags}`.trimEnd())
  return 0
}

// ------------------------------------------------------------------ status / ctl (talk to a running orbit)

/** Connects to the orbit that has this project open (TUI, `orbit up` or daemon), if any. */
async function connectIpc(config: OrbitConfig): Promise<IpcClient | undefined> {
  try {
    return await IpcClient.connect(socketPath(stateDir(config)), 500)
  } catch {
    return undefined
  }
}

const NOT_RUNNING = "orbit is not running for this project (open it with `orbit` or `orbit up`)"

function age(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s`
  return `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}m`
}

export async function runStatus(config: OrbitConfig, json: boolean): Promise<number> {
  const client = await connectIpc(config)
  if (!client) {
    if (json) console.log(JSON.stringify({ running: false, project: config.name, services: [] }))
    else console.error(c.dim(NOT_RUNNING))
    return 1
  }
  try {
    const services = await client.request<ServiceState[]>("snapshot")
    if (json) {
      console.log(JSON.stringify({ running: true, project: config.name, services }, null, 2))
      return 0
    }
    const now = Date.now()
    const memOf = (s: ServiceState) => {
      const mem = s.mem[s.mem.length - 1]
      if (mem === undefined || !s.startedAt) return ""
      const limit = s.resources?.memLimit
      return `${formatBytes(mem)}${limit ? `/${formatBytes(limit)}` : ""}${s.resources?.level === "over" ? " !" : ""}${s.resources?.leak ? " ↗" : ""}`
    }
    const rows = services.map((s) => [s.name, s.status, s.pid ? String(s.pid) : "", s.startedAt && s.pid ? age(now - s.startedAt) : "", memOf(s), s.error ?? s.health ?? ""])
    const header = ["SERVICE", "STATUS", "PID", "UP", "MEM", "DETAIL"]
    const widths = header.map((h, i) => Math.max(h.length, ...rows.map((r) => r[i]!.length)))
    console.log(c.bold(header.map((h, i) => h.padEnd(widths[i]!)).join("  ")))
    for (const [i, r] of rows.entries()) {
      const paint = STATUS_COLOR[services[i]!.status] ?? ((x: string) => x)
      console.log(r.map((v, j) => (j === 1 ? paint(v.padEnd(widths[j]!)) : v.padEnd(widths[j]!))).join("  ").trimEnd())
    }
    return 0
  } catch (err) {
    console.error(c.red((err as Error).message))
    return 1
  } finally {
    client.close()
  }
}

const CTL_ACTIONS = ["start", "stop", "restart", "toggle"] as const

export async function runCtl(config: OrbitConfig, args: string[]): Promise<number> {
  const [action, ...names] = args
  if (!action || !(CTL_ACTIONS as readonly string[]).includes(action)) {
    console.error(c.red(`usage: orbit ctl <${CTL_ACTIONS.join("|")}> [service or group…]`))
    return 1
  }
  for (const n of names) {
    if (!config.services[n] && !config.groups[n]) {
      console.error(c.red(`unknown service or group "${n}"`))
      return 1
    }
  }
  if (!names.length && action !== "start" && action !== "stop") {
    console.error(c.red(`${action} needs at least one service or group`))
    return 1
  }
  const client = await connectIpc(config)
  if (!client) {
    console.error(c.red(NOT_RUNNING))
    return 1
  }
  try {
    const res = names.length
      ? await client.request<{ ok: boolean }>(action as (typeof CTL_ACTIONS)[number], { services: names })
      : await client.request<{ ok: boolean }>(action === "start" ? "startAll" : "stopAll")
    const label = names.length ? names.join(", ") : "all services"
    console.log(res.ok ? c.green(`${action}: ${label}`) : c.red(`${action}: ${label} did not become ready (see \`orbit logs\`)`))
    return res.ok ? 0 : 1
  } catch (err) {
    console.error(c.red((err as Error).message))
    return 1
  } finally {
    client.close()
  }
}

// ------------------------------------------------------------------ up / down

/** `svc │ text` output with a stable color per service, shared by `up` and `logs`. */
function prefixer(names: readonly string[]) {
  const width = Math.max(...names.map((n) => n.length))
  const color = (n: string) => PREFIX_COLORS[names.indexOf(n) % PREFIX_COLORS.length]!
  const print = (service: string, stream: LogLine["stream"], raw: string) => {
    const text = stream === "system" ? c.dim(`» ${raw}`) : stream === "stderr" ? c.yellow(raw) : raw
    process.stdout.write(`${color(service)(`${service.padEnd(width)} │`)} ${text}\n`)
  }
  return { width, color, print }
}

const STATUS_COLOR: Partial<Record<Status, (s: string) => string>> = {
  healthy: c.green,
  running: c.cyan,
  unhealthy: c.yellow,
  crashed: c.red,
  failed: c.red,
  stopped: c.dim,
}

export async function runUp(config: OrbitConfig, names: string[]): Promise<number> {
  for (const n of names) {
    if (!config.services[n] && !config.groups[n]) {
      console.error(c.red(`unknown service or group "${n}"`))
      return 1
    }
  }
  if (await isServed(config)) {
    console.error(c.red("orbit is already running for this project: `orbit ctl start [svc…]` starts services there, `orbit logs -f` follows them"))
    return 1
  }
  const sup = new Supervisor(config)
  process.on("exit", () => sup.killAllSync())
  attachDesktopNotifier(sup)
  const { width, color, print } = prefixer(sup.names)
  sup.logs.onLine((l) => print(l.service, l.stream, l.text))
  const last = new Map<string, Status>()
  sup.on("change", (n?: string) => {
    if (!n) return
    const st = sup.state(n)
    if (last.get(n) === st.status) return
    last.set(n, st.status)
    const paint = STATUS_COLOR[st.status]
    if (paint) process.stdout.write(`${color(n)(`${n.padEnd(width)} │`)} ${paint(`● ${st.status}`)}\n`)
  })

  let stopping = false
  let ipc: IpcServer | undefined
  const stop = async () => {
    if (stopping) return process.exit(130)
    stopping = true
    process.stdout.write(c.dim("\nstopping… (ctrl+c again to force)\n"))
    ipc?.close()
    await sup.dispose()
    process.exit(0)
  }
  process.on("SIGINT", stop)
  process.on("SIGTERM", stop)

  await sup.init()
  try {
    const server = new IpcServer(sup, { onShutdown: () => void stop() })
    if (await server.start()) ipc = server
    else console.error(c.yellow("another orbit started serving this project meanwhile: `orbit status` / `orbit ctl` talk to that one"))
  } catch {}
  const targets = names.flatMap((n) => config.groups[n] ?? [n])
  if (targets.length) await sup.startMany(targets)
  else await sup.startAll()
  await new Promise(() => {}) // run until interrupted
  return 0
}

export async function runDown(config: OrbitConfig): Promise<number> {
  let code = 0
  const dir = stateDir(config)
  // an orbit that is running (TUI or `orbit up`) answers on its socket: ask it to stop everything and quit
  const client = await connectIpc(config)
  if (client) {
    try {
      await client.request("shutdown", { how: "stop" })
    } finally {
      client.close()
    }
    for (let i = 0; i < 300; i++) {
      const again = await connectIpc(config)
      if (!again) break
      again.close()
      await Bun.sleep(100)
    }
    const left = await connectIpc(config)
    left?.close()
    if (left) {
      console.error(c.red("orbit is taking too long to stop"))
      return 1
    }
    console.log(c.green("orbit stopped"))
    return 0
  }
  const holder = readLock(dir)
  if (holder) {
    console.error(c.red(`orbit is open for this project (pid ${holder}) but does not answer: quit it first, or stop services from there`))
    return 1
  }
  // processes a previous orbit session left running
  const saved = readState(dir)
  for (const [name, entry] of Object.entries(saved.services)) {
    const svc = config.services[name]
    if (!svc || svc.type !== "process") continue
    const runner = new ProcessRunner(svc, { log: () => {}, exit: () => {} }, procFiles(dir, name))
    if (await runner.attach(entry)) {
      await runner.stop(svc.stopTimeout)
      console.log(`${name.padEnd(16)} ${c.green("stopped")} ${c.dim(`pid ${entry.pid}`)}`)
    }
    delete saved.services[name]
  }
  writeState(dir, saved)
  for (const svc of Object.values(config.services)) {
    if (svc.type === "docker") {
      const name = containerName(config.name, svc.name)
      const res = await exec(["docker", "rm", "-f", name], { timeout: 30_000 })
      console.log(`${svc.name.padEnd(16)} ${res.code === 0 ? c.green("removed") : c.dim("not running")} ${c.dim(name)}`)
    } else if (svc.type === "compose") {
      const argv = ["docker", "compose", "-f", svc.composeFile!]
      if (svc.composeProject) argv.push("-p", svc.composeProject)
      const res = await exec([...argv, "stop", svc.composeService!], { timeout: 60_000 })
      console.log(`${svc.name.padEnd(16)} ${res.code === 0 ? c.green("stopped") : c.red(res.stderr.trim().split("\n").pop() ?? "error")}`)
      if (res.code !== 0) code = 1
    }
  }
  return code
}

// ------------------------------------------------------------------ init

const SKIP_DIRS = new Set(["node_modules", ".git", "dist", "build", "target", ".next", ".venv", "venv", "vendor", "coverage"])

interface Detected {
  name: string
  cwd: string
  cmd: string
  port?: number
  health?: string
}

function detectPackageJson(dir: string): Detected | undefined {
  const file = join(dir, "package.json")
  if (!existsSync(file)) return
  let pkg: { name?: string; scripts?: Record<string, string>; dependencies?: Record<string, string>; devDependencies?: Record<string, string> }
  try {
    pkg = JSON.parse(readFileSync(file, "utf8"))
  } catch {
    return
  }
  const script = pkg.scripts?.dev ? "dev" : pkg.scripts?.start ? "start" : undefined
  if (!script) return
  const pm = existsSync(join(dir, "bun.lock")) || existsSync(join(dir, "bun.lockb"))
    ? "bun run"
    : existsSync(join(dir, "pnpm-lock.yaml"))
      ? "pnpm"
      : existsSync(join(dir, "yarn.lock"))
        ? "yarn"
        : "npm run"
  const deps = { ...pkg.dependencies, ...pkg.devDependencies }
  const body = pkg.scripts![script]!
  const explicit = /(?:--port[= ]|-p |PORT=)(\d{2,5})/.exec(body)?.[1]
  const port = explicit
    ? Number(explicit)
    : deps.vite ? 5173 : deps.next ? 3000 : deps.astro ? 4321 : deps.nuxt ? 3000 : deps["@angular/core"] ? 4200 : undefined
  return { name: basename(dir), cwd: dir, cmd: `${pm} ${script}`, port }
}

function detectOther(dir: string): Detected | undefined {
  if (existsSync(join(dir, "Cargo.toml"))) return { name: basename(dir), cwd: dir, cmd: "cargo run" }
  if (existsSync(join(dir, "go.mod"))) return { name: basename(dir), cwd: dir, cmd: "go run ." }
  if (existsSync(join(dir, "manage.py"))) return { name: basename(dir), cwd: dir, cmd: "python manage.py runserver", port: 8000 }
  if (existsSync(join(dir, "Procfile"))) {
    const web = /^web:\s*(.+)$/m.exec(readFileSync(join(dir, "Procfile"), "utf8"))
    if (web) return { name: basename(dir), cwd: dir, cmd: web[1]! }
  }
}

function scan(root: string, dir: string, depth: number, out: Detected[]) {
  const found = detectPackageJson(dir) ?? detectOther(dir)
  // a root package.json in a monorepo usually just runs the workspaces
  if (found && !(dir === root && existsSync(join(dir, "package.json")) && depth === 0 && hasChildProjects(dir))) out.push(found)
  if (depth >= 2) return
  for (const entry of readdirSync(dir)) {
    if (entry.startsWith(".") || SKIP_DIRS.has(entry)) continue
    const p = join(dir, entry)
    try {
      if (statSync(p).isDirectory()) scan(root, p, depth + 1, out)
    } catch {}
  }
}

function hasChildProjects(dir: string) {
  return readdirSync(dir).some((e) => {
    if (SKIP_DIRS.has(e) || e.startsWith(".")) return false
    const p = join(dir, e)
    try {
      return statSync(p).isDirectory() && (existsSync(join(p, "package.json")) || readdirSync(p).some((x) => existsSync(join(p, x, "package.json"))))
    } catch {
      return false
    }
  })
}

export function runValidate(opts: LoadOptions): number {
  const { file, config, errors } = validateFile(opts)
  if (errors.length) {
    for (const e of errors) console.error(`${c.red("✖")} ${e}`)
    return 1
  }
  const n = Object.keys(config!.services).length
  const g = Object.keys(config!.groups).length
  console.log(`${c.green("✓")} ${file ? basename(file) : "docker-compose"}: ${n} service${n === 1 ? "" : "s"}, ${g} group${g === 1 ? "" : "s"}`)
  return 0
}

export async function runInit(dir: string, force: boolean): Promise<number> {
  const target = join(dir, "orbit.yaml")
  if (existsSync(target) && !force) {
    console.error(c.red(`${target} already exists (use --force to overwrite)`))
    return 1
  }
  const detected: Detected[] = []
  scan(dir, dir, 0, detected)
  const compose = findComposeFile(dir)
  const composeServices = compose ? parseComposeFile(compose, readFileSync(compose, "utf8")) : []

  const used = new Set(composeServices.map((s) => s.name))
  const services: Record<string, unknown> = {}
  for (const d of detected) {
    let name = d.name.toLowerCase().replace(/[^a-z0-9_-]/g, "-")
    while (used.has(name)) name += "-app"
    used.add(name)
    const entry: Record<string, unknown> = { cmd: d.cmd }
    const rel = relative(dir, d.cwd)
    if (rel) entry.cwd = `./${rel}`
    if (d.port) entry.port = d.port
    // backends usually need the databases of the compose file
    if (composeServices.length && !/web|front|ui|client|app/.test(name)) entry.depends_on = composeServices.map((s) => s.name)
    services[name] = entry
  }

  const doc: Record<string, unknown> = { name: basename(dir) }
  const header = [
    "# yaml-language-server: $schema=https://unpkg.com/@jgoterris/orbit/orbit.schema.json",
    "# orbit.yaml — services managed by orbit",
    "# types: process (cmd), docker (image), compose (imported from docker-compose.yml)",
    "# per service: cmd, cwd, env, port, depends_on, health (http/tcp/cmd), restart (no|on-failure|always)",
    compose ? `# services from ${relative(dir, compose)} are imported automatically: ${composeServices.map((s) => s.name).join(", ")}` : "",
    "",
  ].filter((l) => l !== "")
  if (Object.keys(services).length) doc.services = services
  else
    doc.services = {
      app: { cmd: "echo 'replace me with your dev command'; sleep 3600", port: 3000, health: "http://localhost:3000" },
    }
  writeFileSync(target, header.join("\n") + "\n\n" + YAML.stringify(doc))
  console.log(c.green(`wrote ${target}`))
  for (const d of detected) console.log(`  ${c.bold(d.name)} ${c.dim(d.cmd)}${d.port ? c.dim(` :${d.port}`) : ""}`)
  for (const s of composeServices) console.log(`  ${c.bold(s.name)} ${c.dim("compose")}${s.port ? c.dim(` :${s.port}`) : ""}`)
  console.log(c.dim("review depends_on and ports, then run `orbit`"))
  return 0
}

// ------------------------------------------------------------------ logs

export interface LogsOptions {
  follow: boolean
  lines: number
  grep?: string
  since?: string
}

async function followLive(client: IpcClient, targets: string[], opts: LogsOptions, show: (l: LogLine) => unknown): Promise<number> {
  if (opts.since) console.error(c.yellow("--since is ignored when following a running orbit"))
  let last = 0
  const buffered: LogLine[] = []
  let replaying = true
  client.on("notification", (n: Notification) => {
    if (n.method !== "log") return
    const line = n.params as LogLine
    if (replaying) buffered.push(line)
    else if (line.seq > last) {
      last = line.seq
      show(line)
    }
  })
  const done = new Promise<number>((resolve) =>
    client.on("close", () => {
      console.error(c.dim("orbit closed"))
      resolve(0)
    }),
  )
  await client.request("subscribe", { states: false, logs: true, services: targets })
  const history = (await Promise.all(targets.map((t) => client.request<LogLine[]>("logs", { service: t, limit: opts.lines })))).flat()
  history.sort((a, b) => a.seq - b.seq)
  for (const l of history) {
    last = Math.max(last, l.seq)
    show(l)
  }
  replaying = false
  for (const l of buffered.splice(0)) {
    if (l.seq <= last) continue
    last = l.seq
    show(l)
  }
  const stop = () => {
    client.close()
    process.exit(0)
  }
  process.on("SIGINT", stop)
  process.on("SIGTERM", stop)
  return done
}

export async function runLogs(config: OrbitConfig, names: string[], opts: LogsOptions): Promise<number> {
  for (const n of names) {
    if (!config.services[n] && !config.groups[n]) {
      console.error(c.red(`unknown service or group "${n}"`))
      return 1
    }
  }
  const targets = names.length ? [...new Set(names.flatMap((n) => config.groups[n] ?? [n]))] : Object.keys(config.services)
  const { print } = prefixer(Object.keys(config.services))
  const keep = matcher(opts.grep ?? "")
  // an orbit that is running has everything in memory, system lines and container output included
  const live = opts.follow ? await connectIpc(config) : undefined
  if (live) return followLive(live, targets, opts, (l) => keep(l) && print(l.service, l.stream, l.text))
  const emit = (service: string, stream: LogLine["stream"], raw: string) => {
    const text = cleanLine(raw)
    if (keep({ seq: 0, ts: 0, service, stream, text })) print(service, stream, text)
  }
  const dir = stateDir(config)
  const children: Array<{ kill(): void; exited: Promise<unknown> }> = []
  const tails: FileTail[] = []
  const pending: Promise<void>[] = []
  let warnedSince = false

  for (const name of targets) {
    const svc = config.services[name]!
    if (svc.type === "external") {
      emit(name, "system", "no logs (external service)")
      continue
    }
    if (svc.type === "process") {
      if (opts.since && !warnedSince) {
        warnedSince = true
        console.error(c.yellow("--since is ignored for process services (their log files have no timestamps)"))
      }
      const files = procFiles(dir, name)
      const out = await readTail(files.out, opts.lines)
      const err = await readTail(files.err, opts.lines)
      if (!out.length && !err.length) emit(name, "system", "no logs")
      for (const l of out) emit(name, "stdout", l)
      for (const l of err) emit(name, "stderr", l)
      if (opts.follow) {
        // start at the current end of file: the history was just printed
        for (const [path, stream] of [[files.out, "stdout"], [files.err, "stderr"]] as const) {
          const tail = new FileTail(path, (l) => emit(name, stream, l))
          await tail.start({ bytes: 0, lines: 0 })
          tails.push(tail)
        }
      }
      continue
    }
    const argv =
      svc.type === "docker"
        ? ["docker", "logs"]
        : ["docker", "compose", "-f", svc.composeFile!, ...(svc.composeProject ? ["-p", svc.composeProject] : []), "logs", "--no-color", "--no-log-prefix"]
    argv.push("--tail", String(opts.lines))
    if (opts.since) argv.push("--since", opts.since)
    if (opts.follow) argv.push("-f")
    argv.push(svc.type === "docker" ? containerName(config.name, name) : svc.composeService!)
    const proc = Bun.spawn(argv, { stdin: "ignore", stdout: "pipe", stderr: "pipe" })
    children.push(proc)
    // `docker logs` writes the container's stderr to its own stderr: tell it apart from docker's errors by exit code
    const errLines: string[] = []
    pending.push(
      Promise.all([pipeLines(proc.stdout, (l) => emit(name, "stdout", l)), pipeLines(proc.stderr, (l) => errLines.push(l))]).then(async () => {
        const code = await proc.exited
        for (const l of errLines) emit(name, code === 0 ? "stderr" : "system", l)
      }),
    )
  }
  await Promise.all(pending)

  if (!opts.follow) return 0
  const stop = async () => {
    for (const t of tails) await t.stop(false)
    for (const ch of children) ch.kill()
    process.exit(0)
  }
  process.on("SIGINT", stop)
  process.on("SIGTERM", stop)
  await new Promise(() => {}) // run until interrupted
  return 0
}
