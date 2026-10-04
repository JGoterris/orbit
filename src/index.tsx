#!/usr/bin/env bun
import { statSync } from "node:fs"
import { parseArgs } from "node:util"
import { ConfigError, type OrbitConfig } from "./config/schema.ts"
import { loadConfig } from "./config/load.ts"
import { acquireLock, releaseLock } from "./core/state.ts"
import { Supervisor } from "./core/supervisor.ts"
import { runDown, runGraph, runInit, runList, runLogs, runProjects, runUp } from "./cli.ts"
import { findProject } from "./core/projects.ts"

const HELP = `orbit — launch, control and monitor local services

usage
  orbit [dir]                open the TUI for the orbit.yaml found in dir (or above)
  orbit open <project>       open a remembered project by name or path (P inside orbit switches)
  orbit projects             list the projects orbit remembers
  orbit up [service…]        start services headless, streaming logs (ctrl+c stops)
  orbit logs [service…]      print recent logs (-f to follow, -n lines, --grep, --since)
  orbit down                 stop everything orbit left running (processes, containers)
  orbit graph                print the dependency graph
  orbit ls                   list services
  orbit init [dir]           generate an orbit.yaml by scanning the project

options
  -c, --config <file>        use a specific config file
  -f, --follow               (logs) keep streaming new lines
  -n, --lines <n>            (logs) lines per service, default 200
      --grep <regex>         (logs) only lines matching the regex
      --since <dur>          (logs) docker/compose only, e.g. 10m, 2h
  -u, --up                   (TUI) start all autostart services on launch
  -h, --help                 show this help
`

function fail(msg: string): never {
  console.error(`\x1b[31morbit:\x1b[0m ${msg}\nrun \`orbit --help\` for usage`)
  process.exit(1)
}

let parsed: ReturnType<typeof parse>
function parse() {
  return parseArgs({
  args: Bun.argv.slice(2),
  allowPositionals: true,
  options: {
    config: { type: "string", short: "c" },
    up: { type: "boolean", short: "u" },
    help: { type: "boolean", short: "h" },
    force: { type: "boolean" },
    follow: { type: "boolean", short: "f" },
    lines: { type: "string", short: "n" },
    grep: { type: "string" },
    since: { type: "string" },
  },
  })
}
try {
  parsed = parse()
} catch (err) {
  fail(err instanceof Error ? err.message.split(". ")[0]!.replace(/\.$/, "") : String(err))
}
const { values, positionals } = parsed

if (values.help) {
  console.log(HELP)
  process.exit(0)
}

const [command, ...rest] = positionals
const SUBCOMMANDS = ["up", "down", "logs", "graph", "ls", "init", "open", "projects"]
const sub = command && SUBCOMMANDS.includes(command) ? command : undefined
if (sub === "open" && rest.length !== 1) fail("usage: orbit open <project name or path>")
const opened = sub === "open" ? findProject(rest[0]!) : undefined
if (typeof opened === "string") fail(opened)
const dir = sub === "open" ? opened?.path : sub ? (sub === "init" ? rest[0] : undefined) : command

// Only up/logs take extra positionals (service names); init and open take one; the TUI takes one dir.
const maxExtra = sub === "up" || sub === "logs" ? Infinity : sub === "init" || sub === "open" ? 1 : 0
if (rest.length > maxExtra) fail(`unexpected argument "${rest[maxExtra]}"`)
if (dir !== undefined && sub !== "init") {
  const isDir = (() => {
    try {
      return statSync(dir).isDirectory()
    } catch {
      return false
    }
  })()
  if (!isDir) fail(`unknown command or directory "${dir}"`)
}

function load(allowEmpty = false): OrbitConfig {
  try {
    return loadConfig({ dir, file: values.config, allowEmpty })
  } catch (err) {
    if (err instanceof ConfigError) {
      console.error(`\x1b[31morbit:\x1b[0m ${err.message}`)
      process.exit(1)
    }
    throw err
  }
}

switch (sub) {
  case "projects":
    process.exit(runProjects())
  case "init":
    process.exit(await runInit(dir ?? process.cwd(), !!values.force))
  case "graph":
    process.exit(runGraph(load()))
  case "ls":
    process.exit(runList(load()))
  case "down":
    process.exit(await runDown(load()))
  case "logs": {
    const n = values.lines === undefined ? 200 : Number.parseInt(values.lines, 10)
    if (!Number.isInteger(n) || n < 0) {
      console.error("\x1b[31morbit:\x1b[0m --lines expects a non-negative number")
      process.exit(1)
    }
    process.exit(await runLogs(load(), rest, { follow: !!values.follow, lines: n, grep: values.grep, since: values.since }))
  }
  case "up":
    process.exit(await runUp(load(), rest))
}

// ------------------------------------------------------------------ TUI

const config = load(true)
const { createCliRenderer } = await import("@opentui/core")
const { createRoot } = await import("@opentui/react")
const { ProjectHost } = await import("./ui/ProjectHost.tsx")
const { applyTheme } = await import("./ui/theme.ts")
const { DEFAULT_THEME, THEMES } = await import("./ui/themes.ts")
const { loadCustomThemes, readUserConfig } = await import("./core/userConfig.ts")
const { registerProject } = await import("./core/projects.ts")
const { Session } = await import("./core/session.ts")

const custom = loadCustomThemes()
const themes = { ...THEMES, ...custom.themes }
const themeErrors = custom.errors
let initialTheme = readUserConfig().theme || DEFAULT_THEME
if (!themes[initialTheme]) {
  themeErrors.push(`unknown theme "${initialTheme}", using ${DEFAULT_THEME}`)
  initialTheme = DEFAULT_THEME
}
applyTheme(themes[initialTheme]!)

const first = new Supervisor(config)
const holder = acquireLock(first.stateDir)
if (holder) {
  console.error(`\x1b[31morbit:\x1b[0m already open for this project (pid ${holder}). Quit it first.`)
  process.exit(1)
}
// a folder with neither orbit.yaml nor compose is not a project: show the picker instead of remembering it
const bare = !config.file && !Object.keys(config.services).length && !dir
if (!bare) registerProject(config.root, config.name)
// the project on screen can change (P), so everything below goes through the session
const session = new Session(first)
process.on("exit", () => {
  session.sup.killAllSync()
  releaseLock(session.sup.stateDir)
})

const renderer = await createCliRenderer({ exitOnCtrlC: false, useMouse: true, targetFps: 30 })

let quitting = false
async function quit(code = 0, how: "stop" | "detach" = "stop") {
  if (quitting) return
  quitting = true
  if (how === "detach") session.sup.detach()
  else await Promise.race([session.sup.dispose(), Bun.sleep(20_000)])
  renderer.destroy()
  process.exit(code)
}
for (const sig of ["SIGTERM", "SIGHUP"] as const) process.on(sig, () => void quit(0))
process.on("SIGINT", () => void quit(130))

createRoot(renderer).render(
  <ProjectHost session={session} startWithPicker={bare} onQuit={(how) => quit(0, how)} themes={themes} customThemes={Object.keys(custom.themes)} initialTheme={initialTheme} themeErrors={themeErrors} />,
)

void first.init().then(() => {
  if (values.up) void first.startAll()
})
