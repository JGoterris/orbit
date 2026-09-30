import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs"
import { basename, join, relative } from "node:path"
import YAML from "yaml"
import type { OrbitConfig } from "./config/schema.ts"
import { findComposeFile } from "./config/load.ts"
import { parseComposeFile } from "./config/compose.ts"
import { exec } from "./core/exec.ts"
import { levels, depMapOf } from "./core/graph.ts"
import { containerName } from "./core/runners.ts"
import { Supervisor, type Status } from "./core/supervisor.ts"
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
    s.type === "process" ? s.cmd! : s.type === "docker" ? s.image! : `${relative(config.root, s.composeFile!)}#${s.composeService}`,
  ])
  const header = ["SERVICE", "TYPE", "PORT", "LEVEL", "DEPENDS ON", "RUNS"]
  const widths = header.map((h, i) => Math.max(h.length, ...rows.map((r) => r[i]!.length)))
  const line = (r: string[]) => r.map((v, i) => v.padEnd(widths[i]!)).join("  ")
  console.log(c.bold(line(header)))
  rows.forEach((r) => console.log(line(r)))
  for (const [g, members] of Object.entries(config.groups)) console.log(c.dim(`group ${g}: ${members.join(", ")}`))
  return 0
}

// ------------------------------------------------------------------ up / down

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
  const sup = new Supervisor(config)
  process.on("exit", () => sup.killAllSync())
  const width = Math.max(...sup.names.map((n) => n.length))
  const color = (n: string) => PREFIX_COLORS[sup.names.indexOf(n) % PREFIX_COLORS.length]!
  sup.logs.onLine((l) => {
    const prefix = color(l.service)(`${l.service.padEnd(width)} │`)
    const text = l.stream === "system" ? c.dim(`» ${l.text}`) : l.stream === "stderr" ? c.yellow(l.text) : l.text
    process.stdout.write(`${prefix} ${text}\n`)
  })
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
  const stop = async () => {
    if (stopping) return process.exit(130)
    stopping = true
    process.stdout.write(c.dim("\nstopping… (ctrl+c again to force)\n"))
    await sup.dispose()
    process.exit(0)
  }
  process.on("SIGINT", stop)
  process.on("SIGTERM", stop)

  await sup.init()
  const targets = names.flatMap((n) => config.groups[n] ?? [n])
  if (targets.length) await sup.startMany(targets)
  else await sup.startAll()
  await new Promise(() => {}) // run until interrupted
  return 0
}

export async function runDown(config: OrbitConfig): Promise<number> {
  let code = 0
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
  console.log(c.dim("processes are owned by the orbit session that started them and stop with it"))
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
