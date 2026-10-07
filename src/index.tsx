#!/usr/bin/env bun
import { statSync } from "node:fs"
import { parseArgs } from "node:util"
import { ConfigError, type OrbitConfig } from "./config/schema.ts"
import { loadConfig } from "./config/load.ts"
import pkg from "../package.json"
import { runDaemon } from "./core/ipc/daemon.ts"
import { acquireLock, releaseLock } from "./core/state.ts"
import { Supervisor, type SupervisorLike } from "./core/supervisor.ts"
import { runCtl, runDown, runGraph, runInit, runList, runLogs, runProjects, runStatus, runUp, runValidate } from "./cli.ts"
import { findProject } from "./core/projects.ts"

const HELP = `orbit — launch, control and monitor local services

usage
  orbit [dir]                open the TUI for the orbit.yaml found in dir (or above)
  orbit open <project>       open a remembered project by name or path (P inside orbit switches)
  orbit projects             list the projects orbit remembers
  orbit up [service…]        start services headless, streaming logs (ctrl+c stops)
  orbit logs [service…]      print recent logs (-f to follow, -n lines, --grep, --since)
  orbit down                 stop everything orbit left running (also quits an orbit that is open)
  orbit status [--json]      show the state of the services of an orbit that is running
  orbit ctl <action> [svc…]  start|stop|restart|toggle services of a running orbit (no svc: start/stop all)
  orbit graph                print the dependency graph
  orbit ls                   list services
  orbit init [dir]           generate an orbit.yaml by scanning the project
  orbit validate [dir]       check the orbit.yaml (errors and unknown keys); exit code 1 if invalid
  orbit daemon [dir]         run the supervisor of a project in the background, without UI (the TUI starts it
                             by itself and reconnects to it, so closing the terminal does not stop anything)

options
  -c, --config <file>        use a specific config file
  -f, --follow               (logs) keep streaming new lines
      --json                 (status) machine-readable output
  -n, --lines <n>            (logs) lines per service, default 200
      --grep <regex>         (logs) only lines matching the regex
      --since <dur>          (logs) docker/compose only, e.g. 10m, 2h
  -u, --up                   (TUI) start all autostart services on launch
      --no-daemon            (TUI) run the services inside the TUI itself, as before
      --idle <min>           (daemon) quit after this many minutes with nothing running and nobody connected
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
    json: { type: "boolean" },
    "no-daemon": { type: "boolean" },
    idle: { type: "string" },
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
const SUBCOMMANDS = ["up", "down", "logs", "graph", "ls", "init", "validate", "open", "projects", "status", "ctl", "daemon"]
const sub = command && SUBCOMMANDS.includes(command) ? command : undefined
if (sub === "open" && rest.length !== 1) fail("usage: orbit open <project name or path>")
const opened = sub === "open" ? findProject(rest[0]!) : undefined
if (typeof opened === "string") fail(opened)
const dir = sub === "open" ? opened?.path : sub ? (sub === "init" || sub === "daemon" || sub === "validate" ? rest[0] : undefined) : command

// Only up/logs take extra positionals (service names); init and open take one; the TUI takes one dir.
const maxExtra = sub === "up" || sub === "logs" || sub === "ctl" ? Infinity : sub === "init" || sub === "open" || sub === "daemon" || sub === "validate" ? 1 : 0
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
  case "validate":
    process.exit(runValidate({ dir, file: values.config }))
  case "graph":
    process.exit(runGraph(load()))
  case "ls":
    process.exit(runList(load()))
  case "daemon": {
    const idle = values.idle === undefined ? undefined : Number(values.idle)
    if (idle !== undefined && !(idle > 0)) fail("--idle expects a number of minutes")
    process.exit(await runDaemon(load(), { idleMinutes: idle }))
  }
  case "status":
    process.exit(await runStatus(load(), !!values.json))
  case "ctl":
    process.exit(await runCtl(load(), rest))
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
const { RemoteSupervisor } = await import("./core/ipc/remote.ts")

const custom = loadCustomThemes()
const themes = { ...THEMES, ...custom.themes }
const themeErrors = custom.errors
let initialTheme = readUserConfig().theme || DEFAULT_THEME
if (!themes[initialTheme]) {
  themeErrors.push(`unknown theme "${initialTheme}", using ${DEFAULT_THEME}`)
  initialTheme = DEFAULT_THEME
}
applyTheme(themes[initialTheme]!)

// Services run in an `orbit daemon` that this TUI only mirrors, unless the folder has none (the project picker)
// or the user opted out: then they run in this process, as a plain Supervisor.
const useDaemon = !values["no-daemon"] && Object.keys(config.services).length > 0
let first: SupervisorLike
if (useDaemon) {
  const { connectRemote } = await import("./core/ipc/daemon.ts")
  try {
    const remote = await connectRemote(config)
    // shown once as a toast: an old daemon keeps running its old code after an upgrade
    if (remote.version !== pkg.version) themeErrors.push(`the daemon runs orbit ${remote.version}, this is ${pkg.version}: \`orbit down\` restarts it`)
    first = remote
  } catch (err) {
    console.error(`\x1b[31morbit:\x1b[0m ${(err as Error).message}`)
    process.exit(1)
  }
} else {
  first = new Supervisor(config)
  const holder = acquireLock(first.stateDir)
  if (holder) {
    console.error(`\x1b[31morbit:\x1b[0m already open for this project (pid ${holder}). Quit it first.`)
    process.exit(1)
  }
}
// a folder with neither orbit.yaml nor compose is not a project: show the picker instead of remembering it
const bare = !config.file && !Object.keys(config.services).length && !dir
if (!bare) registerProject(config.root, config.name)
// the project on screen can change (P), so everything below goes through the session
let quitting = false
const session = new Session(first, {
  onShutdown: (how) => void quit(0, how),
  daemon: !values["no-daemon"],
  // the daemon went away (`orbit down`, or it was killed): nothing left to show
  onDisconnect: () => {
    if (quitting) return
    quitting = true
    renderer.destroy()
    console.error("orbit: the daemon stopped")
    process.exit(0)
  },
})
await session.serve()
process.on("exit", () => {
  session.closeIpc()
  session.sup.killAllSync()
  releaseLock(session.sup.stateDir)
})

const renderer = await createCliRenderer({ exitOnCtrlC: false, useMouse: true, targetFps: 30 })

async function quit(code = 0, how: "stop" | "detach" = "stop") {
  if (quitting) return
  quitting = true
  session.closeIpc()
  if (how === "detach") session.sup.detach()
  else await Promise.race([session.sup.dispose(), Bun.sleep(20_000)])
  renderer.destroy()
  process.exit(code)
}
// a signal to a TUI that only mirrors a daemon (closing the terminal sends SIGHUP) must not stop the services
const onSignal = (code: number) => () => void quit(code, session.sup instanceof RemoteSupervisor ? "detach" : "stop")
for (const sig of process.platform === "win32" ? (["SIGTERM", "SIGBREAK"] as const) : (["SIGTERM", "SIGHUP"] as const)) process.on(sig, onSignal(0))
process.on("SIGINT", onSignal(130))

createRoot(renderer).render(
  <ProjectHost session={session} startWithPicker={bare} onQuit={(how) => quit(0, how)} themes={themes} customThemes={Object.keys(custom.themes)} initialTheme={initialTheme} themeErrors={themeErrors} />,
)

void first.init().then(() => {
  if (values.up) void first.startAll()
})
