import { EventEmitter } from "node:events"
import type { Subprocess } from "bun"
import { relative } from "node:path"
import { readEnvFiles } from "../config/envFiles.ts"
import type { Hook, OrbitConfig, ServiceConfig } from "../config/schema.ts"
import { isPortOpen, whoListens } from "./exec.ts"
import { dependentsMap, depMapOf, topoOrder, type DepMap } from "./graph.ts"
import { checkHealth, describeHealth } from "./health.ts"
import { runHooks, type HookPhase } from "./hooks.ts"
import { LogStore } from "./logs.ts"
import { ProcessSampler, sampleContainers } from "./metrics.ts"
import { detectLeak, memLevel, ResourceHistory, type ResourceBucket, type ResourceInfo } from "./resources.ts"
import { createRunner, killGroup, type Runner } from "./runners.ts"
import { FileWatcher } from "./watch.ts"
import { procFiles, readState, stateDir, writeState, type SavedService } from "./state.ts"

export type Status =
  | "stopped" // never started / stopped by the user
  | "waiting" // waiting for dependencies to become ready
  | "starting" // launched, health check not passing yet
  | "running" // up, no health check configured
  | "healthy" // up and health check passing
  | "unhealthy" // up but health check failing
  | "stopping"
  | "exited" // exited on its own with code 0
  | "crashed" // exited on its own with code != 0
  | "failed" // could not be started (dependency failed, port busy, launch error, timeout)

export const UP_STATUSES: ReadonlySet<Status> = new Set(["starting", "running", "healthy", "unhealthy"])
const READY: ReadonlySet<Status> = new Set(["running", "healthy"])
const TERMINAL: ReadonlySet<Status> = new Set(["stopped", "exited", "crashed", "failed"])

/** Ready to be depended on: up (and healthy if checked), or a oneshot task that finished OK. */
export function isReadyStatus(status: Status, oneshot?: boolean): boolean {
  return READY.has(status) || (status === "exited" && !!oneshot)
}

export const HISTORY = 60

export interface ServiceState {
  name: string
  status: Status
  pid?: number
  containerId?: string
  startedAt?: number
  stoppedAt?: number
  exitCode?: number | null
  restarts: number
  error?: string
  health?: string
  /** file watching: undefined when the service has no `watch` */
  watch?: "active" | "paused" | "pending"
  waitingOn?: string[]
  /** attached to a container that was already running: quitting orbit leaves it running */
  adopted?: boolean
  cpu: number[]
  mem: number[]
  /** memory against its limit, and sustained growth; undefined until there is something to say */
  resources?: ResourceInfo
}

/**
 * What the UI and the socket server need from a supervisor: implemented by `Supervisor` (services run in this
 * process) and by `RemoteSupervisor` (they run in an `orbit daemon` and this one mirrors it over the socket).
 */
export interface SupervisorLike extends Pick<EventEmitter, "on" | "off"> {
  readonly config: OrbitConfig
  readonly logs: LogStore
  readonly deps: DepMap
  readonly dependents: Record<string, string[]>
  readonly order: string[]
  readonly stateDir: string
  readonly names: string[]
  service(name: string): ServiceConfig
  state(name: string): ServiceState
  snapshot(): ServiceState[]
  /** Up to an hour of cpu / memory in 10 s buckets, oldest first; `sinceMs` keeps only the last stretch. */
  history(name: string, sinceMs?: number): Promise<ResourceBucket[]>
  isReady(name: string): boolean
  isUp(name: string): boolean
  isAdopted(name: string): boolean
  runningCount(): number
  /** Running services this session started itself (the ones quitting will stop). */
  ownedRunningCount(): number
  init(): Promise<void>
  start(name: string): Promise<boolean>
  stop(name: string): Promise<void>
  restart(name: string): Promise<boolean>
  toggle(name: string): Promise<void>
  startMany(names: string[]): Promise<void>
  startAll(): Promise<void>
  stopAll(): Promise<void>
  toggleWatch(name: string): "paused" | "active" | undefined
  clearLogs(name?: string): void
  /** Stops everything this session owns and lets go (a remote one asks its daemon to quit). */
  dispose(): Promise<void>
  /** Quits without stopping anything. */
  detach(): void
  /** Last resort on process exit; a remote supervisor must not touch the daemon's processes. */
  killAllSync(): void
}

interface Runtime {
  runner?: Runner
  gen: number
  waiters: Array<(ready: boolean) => void>
  healthTimer?: ReturnType<typeof setTimeout>
  restartTimer?: ReturnType<typeof setTimeout>
  watcher?: FileWatcher
  stopPromise?: Promise<void>
  userStopping: boolean
  /** attached to a container that was already running: quitting orbit leaves it running */
  adopted: boolean
  backoff: number
  healthFailures: number
  /** environment the service was last started with (env files included), also given to its hooks */
  env: Record<string, string>
  /** the pre_start / post_start hook process running right now */
  hook?: Subprocess
  /** post_stop of the previous run: a new start waits for it */
  postStop?: Promise<void>
}

export class Supervisor extends EventEmitter implements SupervisorLike {
  readonly logs = new LogStore()
  readonly deps: DepMap
  readonly dependents: Record<string, string[]>
  readonly order: string[]
  private states = new Map<string, ServiceState>()
  private rt = new Map<string, Runtime>()
  private sampler = new ProcessSampler()
  private resHistory = new ResourceHistory()
  private metricsTimer?: ReturnType<typeof setInterval>
  private sampling = false
  private disposed = false

  readonly stateDir: string

  constructor(
    readonly config: OrbitConfig,
    opts: { stateDir?: string } = {},
  ) {
    super()
    this.stateDir = opts.stateDir ?? stateDir(config)
    this.setMaxListeners(100)
    this.deps = depMapOf(config)
    this.dependents = dependentsMap(this.deps)
    this.order = topoOrder(this.deps)
    for (const name of Object.keys(config.services)) {
      this.states.set(name, { name, status: "stopped", restarts: 0, cpu: [], mem: [] })
      this.rt.set(name, { gen: 0, waiters: [], userStopping: false, adopted: false, backoff: 1000, healthFailures: 0, env: {} })
    }
  }

  // ---------------------------------------------------------------- queries

  get names(): string[] {
    return Object.keys(this.config.services)
  }

  service(name: string): ServiceConfig {
    const s = this.config.services[name]
    if (!s) throw new Error(`unknown service ${name}`)
    return s
  }

  state(name: string): ServiceState {
    return this.states.get(name)!
  }

  snapshot(): ServiceState[] {
    return this.names.map((n) => this.states.get(n)!)
  }

  async history(name: string, sinceMs?: number): Promise<ResourceBucket[]> {
    this.service(name)
    return this.resHistory.buckets(name, sinceMs)
  }

  /** Ready to be depended on: up (and healthy if checked), or a oneshot task that finished OK. */
  isReady(name: string, status: Status = this.state(name).status) {
    return isReadyStatus(status, this.service(name).oneshot)
  }

  isUp(name: string) {
    return UP_STATUSES.has(this.state(name).status) || this.state(name).status === "waiting"
  }

  runningCount() {
    return this.names.filter((n) => this.isUp(n) || this.state(n).status === "stopping").length
  }

  /** Running services this session started itself (the ones quitting will stop). */
  ownedRunningCount() {
    return this.names.filter((n) => (this.isUp(n) || this.state(n).status === "stopping") && !this.rt.get(n)!.adopted).length
  }

  isAdopted(name: string) {
    return this.rt.get(name)!.adopted && this.isUp(name)
  }

  // ---------------------------------------------------------------- lifecycle

  /**
   * Re-attaches to what is already running: containers, and processes a previous orbit session left behind
   * (recorded in state.json). Starts the metrics loop.
   */
  async init(): Promise<void> {
    this.metricsTimer = setInterval(() => void this.sampleMetrics(), 2000)
    const saved = readState(this.stateDir).services
    await Promise.all(
      this.names.map(async (name) => {
        const svc = this.service(name)
        const rt = this.rt.get(name)!
        const entry = saved[name]
        if (svc.type === "process" && !entry) return
        const runner = this.makeRunner(name)
        const attached = await runner.attach?.(entry).catch(() => false)
        if (!attached || rt.runner || this.disposed) return
        rt.runner = runner
        // a container orbit did not start itself stays up when orbit quits; anything recorded in state.json is ours
        rt.adopted = svc.type !== "process" && !entry
        const gen = ++rt.gen
        this.log(
          name,
          svc.type === "external"
            ? `monitoring external service (${describeHealth(svc.health)})`
            : svc.type === "process"
            ? `re-attached to running process (pid ${runner.pid})`
            : rt.adopted
              ? "attached to already running container (left running when orbit quits)"
              : "re-attached to running container",
        )
        this.update(name, {
          status: svc.health || (svc.oneshot && svc.type === "process") ? "starting" : "running",
          pid: runner.pid,
          containerId: runner.containerId,
          startedAt: entry?.startedAt ?? Date.now(),
          adopted: rt.adopted,
        })
        if (!svc.oneshot) this.scheduleHealth(name, gen, 0)
      }),
    )
    this.persist()
    this.startWatchers()
  }

  /** Starts a service (and its dependencies first). Resolves true once it is ready. */
  async start(name: string): Promise<boolean> {
    if (this.service(name).type === "external") return this.checkExternal(name)
    const st = this.state(name)
    const rt = this.rt.get(name)!
    if (this.isReady(name)) return true
    if (st.status === "waiting" || st.status === "starting" || st.status === "unhealthy") return this.waitReady(name)
    if (st.status === "stopping") await rt.stopPromise

    clearTimeout(rt.restartTimer)
    const gen = ++rt.gen
    const svc = this.service(name)
    // the previous run's post_stop must be over before this one begins
    if (rt.postStop) await rt.postStop
    if (gen !== rt.gen) return false

    if (svc.dependsOn.length) {
      this.update(name, { status: "waiting", waitingOn: svc.dependsOn, error: undefined })
      const results = await Promise.all(svc.dependsOn.map((d) => this.start(d)))
      if (gen !== rt.gen) return false
      const failed = svc.dependsOn.filter((_, i) => !results[i])
      if (failed.length) {
        this.fail(name, `dependency not ready: ${failed.join(", ")}`)
        return false
      }
    }

    this.update(name, { status: "starting", waitingOn: undefined, error: undefined, exitCode: undefined })
    rt.healthFailures = 0

    if (svc.port && svc.type !== "compose" && (await isPortOpen(svc.port))) {
      if (gen !== rt.gen) return false
      const who = await whoListens(svc.port)
      this.fail(name, `port ${svc.port} is already in use${who ? ` by ${who}` : ""}`)
      return false
    }

    // env files are re-read on every start, so editing a .env only needs a restart
    let env: Record<string, string>
    try {
      const fromFiles = readEnvFiles(svc.envFiles)
      env = { ...fromFiles, ...svc.env }
      if (svc.envFiles.length) {
        const files = svc.envFiles.map((f) => relative(this.config.root, f.path) || f.path).join(", ")
        this.log(name, `env: ${Object.keys(fromFiles).length} vars from ${files}`)
      }
    } catch (err) {
      this.fail(name, (err as Error).message)
      return false
    }

    rt.env = env

    if (svc.hooks?.preStart.length) {
      const res = await this.runHook(name, "pre_start", svc.hooks.preStart)
      if (gen !== rt.gen) return false
      if (!res.ok) {
        this.fail(name, res.error!, false) // runHooks already logged it
        return false
      }
    }

    const runner = this.makeRunner(name, env)
    rt.runner = runner
    rt.adopted = false
    rt.userStopping = false
    this.log(name, svc.type === "process" ? `$ ${svc.cmd}` : `starting ${svc.type} service`)
    try {
      await runner.start()
    } catch (err) {
      if (rt.runner === runner) rt.runner = undefined
      if (gen === rt.gen) this.fail(name, (err as Error).message)
      return false
    }
    if (gen !== rt.gen) return false

    this.update(name, {
      // a oneshot task is "starting" until it exits; its exit code is its health check
      status: svc.health || svc.oneshot ? "starting" : "running",
      pid: runner.pid,
      containerId: runner.containerId,
      startedAt: Date.now(),
      stoppedAt: undefined,
      adopted: false,
    })
    this.persist()
    if (!svc.oneshot) this.scheduleHealth(name, gen, 300)

    const ready = await Promise.race([
      this.waitReady(name),
      Bun.sleep(svc.startTimeout).then(() => "timeout" as const),
    ])
    if (ready === "timeout") {
      if (gen !== rt.gen || this.isReady(name)) return this.isReady(name)
      this.update(name, { status: "unhealthy", error: `not ready after ${Math.round(svc.startTimeout / 1000)}s` })
      this.log(name, `not ready after ${Math.round(svc.startTimeout / 1000)}s (${describeHealth(svc.health)})`)
      this.flushWaiters(name, false)
      return false
    }
    if (ready && gen === rt.gen && svc.hooks?.postStart.length) void this.runPostStart(name, gen)
    return ready
  }

  /** `start` of an external service: check it now. Resolves true once it answers (false after `start_timeout`). */
  private async checkExternal(name: string): Promise<boolean> {
    const svc = this.service(name)
    const rt = this.rt.get(name)!
    if (!rt.runner) {
      // not being monitored yet (init() did not run)
      rt.runner = this.makeRunner(name)
      rt.adopted = true
      rt.gen++
      this.update(name, { status: "starting", startedAt: Date.now(), adopted: true, error: undefined })
    }
    this.scheduleHealth(name, rt.gen, 0)
    const ready = await Promise.race([this.waitReady(name), Bun.sleep(svc.startTimeout).then(() => "timeout" as const)])
    if (ready !== "timeout") return ready
    if (this.isReady(name)) return true
    const detail = this.state(name).health
    this.log(name, `not reachable after ${Math.round(svc.startTimeout / 1000)}s (${describeHealth(svc.health)}${detail ? `: ${detail}` : ""})`)
    this.flushWaiters(name, false)
    return false
  }

  /** Stops a service, stopping everything that depends on it first. */
  async stop(name: string, opts: { dependents?: boolean; keepAdopted?: boolean } = {}): Promise<void> {
    // orbit does not run an external service, so there is nothing to stop (and its dependents keep running)
    if (this.service(name).type === "external") return
    const rt = this.rt.get(name)!
    if (opts.dependents !== false) {
      await Promise.all(
        (this.dependents[name] ?? []).filter((d) => this.isUp(d)).map((d) => this.stop(d, { keepAdopted: opts.keepAdopted })),
      )
    }
    if (opts.keepAdopted && rt.adopted && rt.runner) {
      // only stop following it
      rt.gen++
      clearTimeout(rt.healthTimer)
      rt.runner.killSync()
      rt.runner = undefined
      return
    }
    if (rt.stopPromise) return rt.stopPromise
    clearTimeout(rt.restartTimer)
    rt.gen++
    // a pre_start / post_start still running is cut short
    if (rt.hook) killGroup(rt.hook.pid, "SIGKILL")
    const st = this.state(name)
    const runner = rt.runner
    if (!runner || TERMINAL.has(st.status)) {
      rt.runner = undefined
      if (st.status !== "stopped") this.update(name, { status: "stopped", waitingOn: undefined })
      this.flushWaiters(name, false)
      return
    }
    rt.userStopping = true
    this.update(name, { status: "stopping" })
    this.log(name, "stopping…")
    rt.stopPromise = (async () => {
      try {
        await runner.stop(this.service(name).stopTimeout)
      } catch (err) {
        this.log(name, `stop failed: ${(err as Error).message}`)
      }
      clearTimeout(rt.healthTimer)
      if (rt.runner === runner) rt.runner = undefined
      const postStop = this.service(name).hooks?.postStop
      if (postStop?.length) await this.runHook(name, "post_stop", postStop)
      this.update(name, { status: "stopped", pid: undefined, stoppedAt: Date.now(), resources: undefined })
      this.persist()
      this.log(name, "stopped")
      this.flushWaiters(name, false)
    })().finally(() => (rt.stopPromise = undefined))
    return rt.stopPromise
  }

  async restart(name: string): Promise<boolean> {
    await this.stop(name, { dependents: false })
    return this.start(name)
  }

  /** Toggle: stop if up, start otherwise. */
  async toggle(name: string) {
    if (this.isUp(name)) await this.stop(name)
    else await this.start(name)
  }

  async startMany(names: string[]) {
    await Promise.all(topoOrder(this.deps, names).filter((n) => names.includes(n)).map((n) => this.start(n)))
  }

  async startAll() {
    await this.startMany(this.names.filter((n) => this.service(n).autostart))
  }

  async stopAll() {
    await Promise.all(this.names.map((n) => this.stop(n)))
  }

  /** Stops everything and releases timers. */
  async dispose() {
    this.disposed = true
    clearInterval(this.metricsTimer)
    this.closeWatchers()
    await Promise.all(this.names.map((n) => this.stop(n, { keepAdopted: true })))
    for (const rt of this.rt.values()) {
      clearTimeout(rt.healthTimer)
      clearTimeout(rt.restartTimer)
    }
    this.persist()
  }

  /**
   * Quits without stopping anything: records what is running (state.json) and stops following it.
   * Processes keep running in their own group; the next `init()` picks them up again.
   */
  detach() {
    this.disposed = true
    clearInterval(this.metricsTimer)
    this.closeWatchers()
    this.persist()
    for (const rt of this.rt.values()) {
      clearTimeout(rt.healthTimer)
      clearTimeout(rt.restartTimer)
      rt.gen++
      rt.runner?.release()
      rt.runner = undefined
      for (const w of rt.waiters.splice(0)) w(false)
    }
  }

  /** Last resort on process exit: SIGKILL every process group and watcher we own. Synchronous. */
  killAllSync() {
    for (const rt of this.rt.values()) {
      rt.runner?.killSync()
      if (rt.hook) killGroup(rt.hook.pid, "SIGKILL")
    }
  }

  /** Pauses / resumes file watching of a service. Undefined if it has no `watch`. */
  toggleWatch(name: string): "paused" | "active" | undefined {
    const watcher = this.rt.get(name)!.watcher
    if (!watcher) return undefined
    watcher.paused = !watcher.paused
    this.log(name, watcher.paused ? "watch paused" : "watch resumed")
    this.update(name, { watch: watcher.paused ? "paused" : "active" })
    return watcher.paused ? "paused" : "active"
  }

  clearLogs(name?: string) {
    this.logs.clear(name)
    this.emit("change", name)
  }

  // ---------------------------------------------------------------- internals

  private startWatchers() {
    for (const name of this.names) {
      const cfg = this.service(name).watch
      const rt = this.rt.get(name)!
      if (!cfg || rt.watcher || this.disposed) continue
      rt.watcher = new FileWatcher(this.service(name).cwd, cfg, {
        onTrigger: (files) => this.onWatchTrigger(name, files),
        onError: (msg) => this.log(name, msg),
        onPending: (pending) => {
          if (!rt.watcher?.paused) this.update(name, { watch: pending ? "pending" : "active" })
        },
      })
      rt.watcher.start()
      this.update(name, { watch: "active" })
    }
  }

  private closeWatchers() {
    for (const rt of this.rt.values()) {
      rt.watcher?.close()
      rt.watcher = undefined
    }
  }

  private onWatchTrigger(name: string, files: string[]) {
    const status = this.state(name).status
    // a change never starts a service the user stopped, nor interrupts one still waiting for its dependencies
    if (status === "stopped" || status === "stopping" || status === "waiting" || this.disposed) return
    const shown = files.slice(0, 2).join(", ") + (files.length > 2 ? ` (+${files.length - 2} more)` : "")
    this.log(name, `↻ ${shown} changed — restarting`)
    this.rt.get(name)!.backoff = 1000
    void this.restart(name)
  }

  /** Writes what this session owns and has running, for a later session to re-attach to. */
  private persist() {
    const services: Record<string, SavedService> = {}
    for (const name of this.names) {
      const rt = this.rt.get(name)!
      if (!rt.runner || rt.adopted) continue
      const pid = rt.runner.pid
      if (this.service(name).type === "process" && pid === undefined) continue
      services[name] = { pid, startTime: rt.runner.startTime, startedAt: this.state(name).startedAt ?? Date.now() }
    }
    writeState(this.stateDir, { services })
  }

  private runHook(name: string, phase: HookPhase, hooks: Hook[], exitCode?: number | null) {
    const rt = this.rt.get(name)!
    return runHooks(phase, hooks, {
      service: name,
      cwd: this.service(name).cwd,
      env: rt.env,
      exitCode,
      log: (stream, text) => this.logs.append(name, stream, text),
      track: (proc) => {
        rt.hook = proc
      },
    })
  }

  /** post_start never blocks readiness: a failure is reported but the service stays up. */
  private async runPostStart(name: string, gen: number) {
    const rt = this.rt.get(name)!
    const res = await this.runHook(name, "post_start", this.service(name).hooks!.postStart)
    if (!res.ok && gen === rt.gen) this.update(name, { error: res.error })
  }

  private makeRunner(name: string, env?: Record<string, string>): Runner {
    const svc = this.service(name)
    const runner: Runner = createRunner(env ? { ...svc, env } : svc, this.config.name, {
      log: (stream, text) => this.logs.append(name, stream, text),
      exit: (code, signal) => this.onExit(name, runner, code, signal),
    }, procFiles(this.stateDir, name))
    return runner
  }

  private onExit(name: string, runner: Runner, code: number | null, signal?: string | null) {
    const rt = this.rt.get(name)!
    if (rt.runner !== runner || rt.userStopping) return
    rt.runner = undefined
    rt.gen++
    clearTimeout(rt.healthTimer)
    this.persist()
    const st = this.state(name)
    const svc = this.service(name)
    const ok = code === 0
    const uptime = st.startedAt ? Date.now() - st.startedAt : 0
    this.log(
      name,
      svc.oneshot && ok
        ? `✓ done in ${(uptime / 1000).toFixed(1)}s`
        : `exited with ${signal ? `signal ${signal}` : `code ${code}`}${uptime ? ` after ${Math.round(uptime / 1000)}s` : ""}`,
    )
    this.update(name, {
      status: ok ? "exited" : "crashed",
      exitCode: code,
      pid: undefined,
      stoppedAt: Date.now(),
      resources: undefined,
      error: ok ? undefined : `exited with ${signal ?? `code ${code}`}`,
    })
    this.flushWaiters(name, false)

    // post_stop also runs when the service went down on its own; a restart waits for it (see start())
    if (svc.hooks?.postStop.length) {
      rt.postStop = this.runHook(name, "post_stop", svc.hooks.postStop, code)
        .then(() => {})
        .finally(() => (rt.postStop = undefined))
    }

    // a oneshot that succeeded is finished, whatever the restart policy says
    if ((svc.restart === "always" && !(svc.oneshot && ok)) || (svc.restart === "on-failure" && !ok)) {
      if (uptime > 30_000) rt.backoff = 1000
      const delay = rt.backoff
      rt.backoff = Math.min(rt.backoff * 2, 30_000)
      this.log(name, `restarting in ${delay / 1000}s (restart: ${svc.restart})`)
      rt.restartTimer = setTimeout(() => {
        this.update(name, { restarts: this.state(name).restarts + 1 })
        void this.start(name)
      }, delay)
    }
  }

  private scheduleHealth(name: string, gen: number, delay: number) {
    const svc = this.service(name)
    const rt = this.rt.get(name)!
    if (!svc.health) return
    clearTimeout(rt.healthTimer)
    rt.healthTimer = setTimeout(async () => {
      if (gen !== rt.gen || !rt.runner) return
      const res = await checkHealth(svc.health!, svc, rt.runner.containerId).catch((e) => ({
        ok: false,
        detail: String(e),
      }))
      if (gen !== rt.gen || !rt.runner) return
      const st = this.state(name)
      if (res.ok) {
        rt.healthFailures = 0
        if (st.status !== "healthy") {
          this.log(name, `healthy (${describeHealth(svc.health)}: ${res.detail})`)
          this.update(name, { status: "healthy", health: res.detail, error: undefined })
        } else if (st.health !== res.detail) this.update(name, { health: res.detail })
      } else {
        rt.healthFailures++
        if (svc.type === "external" && st.status === "starting") {
          // nothing else will ever move an external service out of "starting"
          this.log(name, `unreachable: ${res.detail}`)
          this.update(name, { status: "unhealthy", health: res.detail })
        } else
        // a service that was healthy needs a few consecutive failures to be marked unhealthy
        if (st.status === "healthy" && rt.healthFailures >= 3) {
          this.log(name, `health check failing: ${res.detail}`)
          this.update(name, { status: "unhealthy", health: res.detail })
        } else if (st.health !== res.detail) this.update(name, { health: res.detail })
      }
      // poll faster while starting
      const next = this.state(name).status === "starting" ? Math.min(svc.health!.interval, 1000) : svc.health!.interval
      this.scheduleHealth(name, gen, next)
    }, delay)
  }

  private waitReady(name: string): Promise<boolean> {
    const st = this.state(name)
    if (this.isReady(name)) return Promise.resolve(true)
    if (TERMINAL.has(st.status)) return Promise.resolve(false)
    return new Promise((resolve) => this.rt.get(name)!.waiters.push(resolve))
  }

  private flushWaiters(name: string, ready: boolean) {
    const rt = this.rt.get(name)!
    const waiters = rt.waiters
    rt.waiters = []
    waiters.forEach((w) => w(ready))
  }

  private fail(name: string, error: string, log = true) {
    if (log) this.log(name, `✖ ${error}`)
    this.update(name, { status: "failed", error, waitingOn: undefined })
    this.flushWaiters(name, false)
  }

  private update(name: string, patch: Partial<ServiceState>) {
    const prev = this.states.get(name)!
    const next = { ...prev, ...patch }
    this.states.set(name, next)
    if (this.isReady(name, next.status) && !this.isReady(name, prev.status)) this.flushWaiters(name, true)
    this.emit("change", name)
  }

  log(name: string, text: string) {
    this.logs.append(name, "system", text)
  }

  /** The `resources` of a service after a sample; the previous object when nothing changed (no needless re-render / broadcast). */
  private assess(name: string, mem: number, containerLimit?: number): ResourceInfo | undefined {
    const st = this.state(name)
    const svc = this.service(name)
    const prev = st.resources
    const memLimit = svc.memLimit ?? containerLimit
    const level = memLevel(mem, memLimit, prev?.level)
    const since = (st.startedAt ?? 0) - 10_000
    const leak = svc.leakDetection
      ? detectLeak(this.resHistory.buckets(name).filter((b) => b.t >= since), memLimit)
      : undefined
    // the estimate moves every sample; only a leak that appears, disappears or changes a lot is news
    const keepLeak = prev?.leak && leak && Math.abs(leak.perMin - prev.leak.perMin) < prev.leak.perMin * 0.25 ? prev.leak : leak
    if (!memLimit && !level && !keepLeak) return undefined
    if (prev && prev.memLimit === memLimit && prev.level === level && prev.leak === keepLeak) return prev
    return { memLimit, level, leak: keepLeak }
  }

  private async sampleMetrics() {
    if (this.sampling) return
    this.sampling = true
    try {
      const procs: Array<[string, number]> = []
      const containers: Array<[string, string]> = []
      for (const st of this.states.values()) {
        if (!UP_STATUSES.has(st.status)) continue
        if (st.pid) procs.push([st.name, st.pid])
        else if (st.containerId) containers.push([st.name, st.containerId])
      }
      const procSamples = this.sampler.sample(procs.map(([, p]) => p))
      const push = (name: string, cpu: number, mem: number, containerLimit?: number) => {
        const st = this.state(name)
        this.resHistory.push(name, cpu, mem)
        this.update(name, {
          cpu: [...st.cpu, cpu].slice(-HISTORY),
          mem: [...st.mem, mem].slice(-HISTORY),
          resources: this.assess(name, mem, containerLimit),
        })
      }
      for (const [name, pid] of procs) {
        const s = procSamples.get(pid)
        if (s) push(name, s.cpu, s.mem)
      }
      if (containers.length) {
        const samples = await sampleContainers(containers.map(([, id]) => id))
        for (const [name, id] of containers) {
          const s = samples.get(id)
          if (s && UP_STATUSES.has(this.state(name).status)) push(name, s.cpu, s.mem, s.limit)
        }
      }
    } finally {
      this.sampling = false
    }
  }
}
