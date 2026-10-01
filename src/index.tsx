#!/usr/bin/env bun
import { parseArgs } from "node:util"
import { ConfigError, type OrbitConfig } from "./config/schema.ts"
import { loadConfig } from "./config/load.ts"
import { acquireLock, releaseLock } from "./core/state.ts"
import { Supervisor } from "./core/supervisor.ts"
import { runDown, runGraph, runInit, runList, runLogs, runUp } from "./cli.ts"

const HELP = `orbit — launch, control and monitor local services

usage
  orbit [dir]                open the TUI for the orbit.yaml found in dir (or above)
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

const { values, positionals } = parseArgs({
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

if (values.help) {
  console.log(HELP)
  process.exit(0)
}

const [command, ...rest] = positionals
const SUBCOMMANDS = ["up", "down", "logs", "graph", "ls", "init"]
const sub = command && SUBCOMMANDS.includes(command) ? command : undefined
const dir = sub ? (sub === "init" ? rest[0] : undefined) : command

function load(): OrbitConfig {
  try {
    return loadConfig({ dir, file: values.config })
  } catch (err) {
    if (err instanceof ConfigError) {
      console.error(`\x1b[31morbit:\x1b[0m ${err.message}`)
      process.exit(1)
    }
    throw err
  }
}

switch (sub) {
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

const config = load()
const { createCliRenderer } = await import("@opentui/core")
const { createRoot } = await import("@opentui/react")
const { App } = await import("./ui/App.tsx")

const sup = new Supervisor(config)
const holder = acquireLock(sup.stateDir)
if (holder) {
  console.error(`\x1b[31morbit:\x1b[0m already open for this project (pid ${holder}). Quit it first.`)
  process.exit(1)
}
process.on("exit", () => {
  sup.killAllSync()
  releaseLock(sup.stateDir)
})

const renderer = await createCliRenderer({ exitOnCtrlC: false, useMouse: true, targetFps: 30 })

let quitting = false
async function quit(code = 0, how: "stop" | "detach" = "stop") {
  if (quitting) return
  quitting = true
  if (how === "detach") sup.detach()
  else await Promise.race([sup.dispose(), Bun.sleep(20_000)])
  renderer.destroy()
  process.exit(code)
}
for (const sig of ["SIGTERM", "SIGHUP"] as const) process.on(sig, () => void quit(0))
process.on("SIGINT", () => void quit(130))

createRoot(renderer).render(<App sup={sup} onQuit={(how) => quit(0, how)} />)

void sup.init().then(() => {
  if (values.up) void sup.startAll()
})
