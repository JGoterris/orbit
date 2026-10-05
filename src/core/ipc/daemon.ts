import { mkdirSync, openSync, closeSync } from "node:fs"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import type { OrbitConfig } from "../../config/schema.ts"
import { acquireLock, readLock, releaseLock, stateDir } from "../state.ts"
import { Supervisor } from "../supervisor.ts"
import { IpcClient } from "./client.ts"
import { socketPath } from "./endpoint.ts"
import { RemoteSupervisor } from "./remote.ts"
import { IpcServer } from "./server.ts"

/** The daemon re-attaches to containers before it listens, which can take a few seconds. */
const STARTUP_TIMEOUT = 20_000
const ENTRY = fileURLToPath(new URL("../../index.tsx", import.meta.url))

const stamp = (msg: string) => console.log(`${new Date().toISOString()} ${msg}`)

/**
 * `orbit daemon`: the supervisor of a project without a UI. Owns the project's lock and its socket, keeps
 * health checks, restarts and watchers running while clients come and go. Resolves with the exit code once
 * a client asks it to quit (`shutdown`) or after `idleMinutes` with nothing running and nobody connected.
 */
export async function runDaemon(config: OrbitConfig, opts: { idleMinutes?: number } = {}): Promise<number> {
  const sup = new Supervisor(config)
  const holder = acquireLock(sup.stateDir)
  if (holder) {
    console.error(`already open for this project (pid ${holder})`)
    return 1
  }
  process.on("SIGHUP", () => {}) // the terminal that spawned us may go away
  process.on("exit", () => {
    sup.killAllSync()
    releaseLock(sup.stateDir)
  })

  let finish!: (code: number) => void
  const done = new Promise<number>((resolve) => (finish = resolve))
  let ending = false
  const quit = async (how: "stop" | "detach", why: string) => {
    if (ending) return
    ending = true
    stamp(`quitting (${why}, ${how})`)
    ipc.close()
    if (how === "detach") sup.detach()
    else await Promise.race([sup.dispose(), Bun.sleep(20_000)])
    finish(0)
  }
  const ipc = new IpcServer(sup, { onShutdown: (how) => void quit(how, "asked by a client") })
  process.on("SIGTERM", () => void quit("stop", "SIGTERM"))
  process.on("SIGINT", () => void quit("stop", "SIGINT"))

  // init first: a client that connects must not race the re-attach of what a previous session left running
  await sup.init()
  if (!(await ipc.start())) {
    console.error("another orbit already serves this project")
    releaseLock(sup.stateDir)
    return 1
  }
  stamp(`orbit daemon for ${config.name} ready (pid ${process.pid}, socket ${ipc.path})`)

  if (opts.idleMinutes) {
    let idleSince: number | undefined
    const timer = setInterval(() => {
      if (ipc.clientCount > 0 || sup.runningCount() > 0) return void (idleSince = undefined)
      idleSince ??= Date.now()
      if (Date.now() - idleSince >= opts.idleMinutes! * 60_000) void quit("stop", `idle for ${opts.idleMinutes} min`)
    }, 10_000)
    timer.unref()
  }
  return done
}

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

/**
 * Connects to the orbit that serves this project, starting an `orbit daemon` for it when there is none.
 * Rejects with a readable message when the project is open somewhere that does not answer.
 */
export async function connectRemote(config: OrbitConfig): Promise<RemoteSupervisor> {
  const dir = stateDir(config)
  const path = socketPath(dir)
  const attempt = () => RemoteSupervisor.connect(path).catch((err: NodeJS.ErrnoException) => {
    if (err.code === "ENOENT" || err.code === "ECONNREFUSED" || /timeout/.test(err.message)) return undefined
    throw err
  })

  let remote = await attempt()
  if (remote) return remote

  // someone holds the lock but does not listen yet (a daemon still starting): wait for it instead of racing it
  let spawned = false
  if (!readLock(dir)) {
    mkdirSync(dir, { recursive: true })
    const log = openSync(join(dir, "daemon.log"), "a")
    try {
      // `-c` when the project came from a config file, the folder otherwise: either way it is the same project
      const target = config.file ? ["-c", config.file] : [config.root]
      Bun.spawn([process.execPath, ENTRY, "daemon", ...target], {
        cwd: config.root,
        env: process.env, // explicit: Bun does not pick up changes made to process.env after startup
        stdin: "ignore",
        stdout: log,
        stderr: log,
        detached: true, // its own session: closing our terminal must not take it down
      }).unref()
      spawned = true
    } finally {
      closeSync(log)
    }
  }

  const began = Date.now()
  while (Date.now() - began < STARTUP_TIMEOUT) {
    await wait(50)
    remote = await attempt()
    if (remote) return remote
    if (spawned && Date.now() - began > 1500 && !readLock(dir)) break // it took no lock: it died on startup
  }
  const holder = readLock(dir)
  throw new Error(
    holder && !spawned
      ? `orbit is open for this project (pid ${holder}) but does not answer`
      : `the orbit daemon did not start; see ${join(dir, "daemon.log")}`,
  )
}

/** True when an orbit already serves this project's socket. */
export async function isServed(config: OrbitConfig): Promise<boolean> {
  try {
    const c = await IpcClient.connect(socketPath(stateDir(config)), 500)
    c.close()
    return true
  } catch {
    return false
  }
}
