import { closeSync, existsSync, mkdirSync, openSync, readFileSync, rmSync } from "node:fs"
import { dirname, join } from "node:path"
import type { Subprocess } from "bun"
import type { ServiceConfig } from "../config/schema.ts"
import { exec, sleep } from "./exec.ts"
import { FileTail, pipeLines, type LogStream } from "./logs.ts"
import { cheapStartTime, isWindows, killTree, pidAlive, procStartTime, shellArgv, treeAlive } from "./platform/index.ts"
import type { ProcFiles, SavedService } from "./state.ts"

export interface RunnerCallbacks {
  log(stream: LogStream, text: string): void
  /** The service stopped (on its own or because we stopped it). */
  exit(code: number | null, signal?: string | null): void
}

export interface Runner {
  /** Launches the service. Resolves once it is launched (not necessarily ready). */
  start(): Promise<void>
  /** Re-attaches to an instance that is already running (a container, or a process saved by a previous session). */
  attach?(saved?: SavedService): Promise<boolean>
  stop(timeoutMs: number): Promise<void>
  /** Stops following the service (logs, exit watchers) without touching it: it keeps running. */
  release(): void
  /** Synchronous last-resort cleanup on process exit. */
  killSync(): void
  readonly pid?: number
  readonly startTime?: number
  readonly containerId?: string
}

export function createRunner(svc: ServiceConfig, project: string, cb: RunnerCallbacks, files: ProcFiles): Runner {
  switch (svc.type) {
    case "process":
      return new ProcessRunner(svc, cb, files)
    case "docker":
      return new DockerRunner(svc, project, cb)
    case "compose":
      return new ComposeRunner(svc, cb)
    case "external":
      return new ExternalRunner()
  }
}

/** Nothing to run: the supervisor only health-checks an external service. */
export class ExternalRunner implements Runner {
  async start() {}
  async attach() {
    return true
  }
  async stop() {}
  release() {}
  killSync() {}
}

/** Tiny shell-words splitter (quotes and backslashes), for docker commands. */
export function splitArgs(cmd: string): string[] {
  const out: string[] = []
  let cur = ""
  let quote: string | null = null
  let has = false
  for (let i = 0; i < cmd.length; i++) {
    const c = cmd[i]!
    if (quote) {
      if (c === quote) quote = null
      else if (c === "\\" && quote === '"' && i + 1 < cmd.length) cur += cmd[++i]
      else cur += c
    } else if (c === "'" || c === '"') {
      quote = c
      has = true
    } else if (c === "\\" && i + 1 < cmd.length) {
      cur += cmd[++i]
      has = true
    } else if (/\s/.test(c)) {
      if (cur || has) out.push(cur)
      cur = ""
      has = false
    } else {
      cur += c
    }
  }
  if (cur || has) out.push(cur)
  return out
}

/** Runs the command and leaves its exit code in a file, so a later orbit session can tell how it ended (POSIX; Windows uses platform/wrap.ts). */
const WRAPPER = 'sh -c "$1"; c=$?; echo $c > "$2"; exit $c'
const WIN_WRAPPER = join(import.meta.dir, "platform", "wrap.ts")
const BACKLOG = { bytes: 64 * 1024, lines: 200 }

/**
 * Output goes to files instead of pipes, and the process lives in its own group, so it can outlive orbit:
 * a new session re-attaches with `attach()` (pid + start time), re-reads the logs and watches it from there.
 */
export class ProcessRunner implements Runner {
  private proc?: Subprocess<"ignore", number, number>
  private _pid?: number
  private _startTime?: number
  private tails: FileTail[] = []
  private watcher?: ReturnType<typeof setInterval>
  private released = false
  private verified = false

  constructor(
    private svc: ServiceConfig,
    private cb: RunnerCallbacks,
    private files: ProcFiles,
  ) {}

  get pid() {
    return this._pid
  }

  get startTime() {
    return this._startTime
  }

  async start() {
    if (!existsSync(this.svc.cwd)) throw new Error(`working directory does not exist: ${this.svc.cwd}`)
    this.released = false
    mkdirSync(dirname(this.files.out), { recursive: true })
    rmSync(this.files.exit, { force: true })
    const out = openSync(this.files.out, "w")
    const err = openSync(this.files.err, "w")
    let proc: Subprocess<"ignore", number, number>
    try {
      const argv = isWindows
        ? [process.execPath, WIN_WRAPPER, this.files.exit, this.svc.cmd!, ...(this.svc.shell ? [this.svc.shell] : [])]
        : this.svc.shell
          ? [...shellArgv(WRAPPER, this.svc.shell), "orbit", this.svc.cmd!, this.files.exit]
          : ["/bin/sh", "-c", WRAPPER, "orbit", this.svc.cmd!, this.files.exit]
      proc = Bun.spawn(argv, {
        cwd: this.svc.cwd,
        env: { ...process.env, FORCE_COLOR: "1", ...this.svc.env },
        stdin: "ignore",
        stdout: out,
        stderr: err,
        // own process group, so we can signal the whole tree (npm -> node -> esbuild...)
        detached: true,
      })
    } finally {
      closeSync(out)
      closeSync(err)
    }
    this.proc = proc
    this._pid = proc.pid
    this._startTime = procStartTime(proc.pid)
    this.verified = true
    await this.follow()
    proc.exited.then(async (code) => {
      // leftovers of the group (e.g. a dev server's workers) must not keep ports busy
      if (treeAlive(proc.pid)) killTree(proc.pid, "SIGTERM")
      if (this.proc !== proc || this.released) return
      await this.stopTails()
      if (this.proc === proc && !this.released) this.cb.exit(code, proc.signalCode)
    })
  }

  async attach(saved?: SavedService) {
    if (!saved?.pid || !this.isSame(saved.pid, saved.startTime)) return false
    this._pid = saved.pid
    this._startTime = saved.startTime
    this.released = false
    await this.follow(BACKLOG)
    this.watcher = setInterval(() => void this.checkAdopted(), 1000)
    return true
  }

  private async follow(backlog?: typeof BACKLOG) {
    this.tails = [
      new FileTail(this.files.out, (l) => this.cb.log("stdout", l)),
      new FileTail(this.files.err, (l) => this.cb.log("stderr", l)),
    ]
    await Promise.all(this.tails.map((t) => t.start(backlog)))
  }

  private draining?: Promise<unknown>

  private async stopTails() {
    const tails = this.tails
    this.tails = []
    if (tails.length) this.draining = Promise.all(tails.map((t) => t.stop()))
    await this.draining
  }

  /** Is `pid` still the process we launched (not a recycled pid)? */
  private isSame(pid: number, startTime?: number): boolean {
    // where reading the start time spawns a process, only the first check (attach) pays for it; polling just asks the pid
    if (!cheapStartTime && this.verified) return pidAlive(pid)
    const now = procStartTime(pid)
    if (now !== undefined) return (this.verified = startTime === undefined || now === startTime)
    // the start time is unavailable (the process is gone, or the platform cannot say): fall back to "the tree exists"
    return !cheapStartTime && treeAlive(pid)
  }

  private leaderAlive(): boolean {
    return this._pid !== undefined && this.isSame(this._pid, this._startTime)
  }

  private async checkAdopted() {
    if (this.released || this.leaderAlive()) return
    clearInterval(this.watcher)
    const pid = this._pid!
    if (treeAlive(pid)) killTree(pid, "SIGTERM")
    await this.stopTails()
    if (this.released) return
    let code: number | null = null
    try {
      const n = Number.parseInt(readFileSync(this.files.exit, "utf8"), 10)
      if (Number.isInteger(n)) code = n
    } catch {}
    this.cb.exit(code)
  }

  release() {
    this.released = true
    clearInterval(this.watcher)
    for (const t of this.tails) void t.stop(false)
    this.tails = []
    this.proc?.unref()
  }

  killSync() {
    if (!this.released && this._pid !== undefined && this.leaderAlive()) killTree(this._pid, "SIGKILL")
  }

  private waitLeader(ms: number): Promise<boolean> {
    if (this.proc) return Promise.race([this.proc.exited.then(() => true), sleep(ms).then(() => false)])
    return (async () => {
      const end = Date.now() + ms
      while (this.leaderAlive()) {
        if (Date.now() >= end) return false
        await sleep(25)
      }
      return true
    })()
  }

  async stop(timeoutMs: number) {
    const pid = this._pid
    if (pid === undefined || this.released) return
    const running = this.proc ? this.proc.exitCode === null && this.proc.signalCode === null : this.leaderAlive()
    if (!running) return
    clearInterval(this.watcher)
    killTree(pid, "SIGTERM")
    if (!(await this.waitLeader(timeoutMs))) {
      this.cb.log("system", `did not stop after ${timeoutMs}ms, sending SIGKILL`)
      killTree(pid, "SIGKILL")
      await this.waitLeader(5000)
    }
    if (treeAlive(pid)) {
      // the main process exited but left group members behind (e.g. background jobs);
      // the leader is already gone, so wait for the group itself to disappear.
      this.cb.log("system", "killing leftover child processes")
      killTree(pid, "SIGKILL")
      for (let i = 0; i < 50 && treeAlive(pid); i++) await sleep(10)
    }
    await this.stopTails()
  }
}

/** Shared container plumbing: follow logs and wait for the exit code of a container id. */
abstract class ContainerRunner implements Runner {
  containerId?: string
  private followers: Subprocess[] = []
  private generation = 0

  constructor(
    protected svc: ServiceConfig,
    protected cb: RunnerCallbacks,
  ) {}

  abstract start(): Promise<void>
  abstract attach(saved?: SavedService): Promise<boolean>
  abstract stop(timeoutMs: number): Promise<void>

  /** Streams a launcher command's output (e.g. image pulls) into the logs. */
  protected async runStreaming(argv: string[], cwd?: string): Promise<{ code: number; stdout: string }> {
    const proc = Bun.spawn(argv, { cwd, stdin: "ignore", stdout: "pipe", stderr: "pipe" })
    let stdout = ""
    await Promise.all([
      pipeLines(proc.stdout, (l) => (stdout += l + "\n")),
      pipeLines(proc.stderr, (l) => this.cb.log("system", l)),
    ])
    return { code: await proc.exited, stdout }
  }

  protected follow(id: string, tail = 200) {
    this.detach()
    const gen = ++this.generation
    this.containerId = id
    // detached: a ctrl+c in the terminal must not kill our watchers before we stop the container
    const logs = Bun.spawn(["docker", "logs", "-f", "--tail", String(tail), id], {
      stdin: "ignore",
      stdout: "pipe",
      stderr: "pipe",
      detached: true,
    })
    pipeLines(logs.stdout, (l) => this.cb.log("stdout", l))
    pipeLines(logs.stderr, (l) => this.cb.log("stderr", l))
    const wait = Bun.spawn(["docker", "wait", id], { stdin: "ignore", stdout: "pipe", stderr: "ignore", detached: true })
    this.followers = [logs, wait]
    new Response(wait.stdout).text().then(async (out) => {
      await wait.exited
      if (gen !== this.generation) return // detached on purpose
      const code = Number.parseInt(out.trim(), 10)
      if (Number.isNaN(code)) {
        // the watcher died, not necessarily the container
        const res = await exec(["docker", "inspect", "-f", "{{.State.Running}} {{.State.ExitCode}}", id], { timeout: 5000 })
        const [running, exitCode] = res.stdout.trim().split(" ")
        if (gen !== this.generation) return
        if (running === "true") return this.follow(id, 0)
        return this.cb.exit(exitCode !== undefined && exitCode !== "" ? Number(exitCode) : null)
      }
      this.cb.exit(code)
    })
  }

  killSync() {
    this.detach()
  }

  release() {
    this.detach()
  }

  protected detach() {
    this.generation++
    for (const p of this.followers) p.kill()
    this.followers = []
  }
}

export function containerName(project: string, service: string) {
  return `orbit-${project}-${service}`.replace(/[^a-zA-Z0-9_.-]/g, "-").toLowerCase()
}

export class DockerRunner extends ContainerRunner {
  private name: string

  constructor(svc: ServiceConfig, project: string, cb: RunnerCallbacks) {
    super(svc, cb)
    this.name = containerName(project, svc.name)
  }

  async attach() {
    const res = await exec(["docker", "inspect", "-f", "{{.State.Running}} {{.Id}}", this.name], { timeout: 5000 })
    const [running, id] = res.stdout.trim().split(" ")
    if (res.code !== 0 || running !== "true" || !id) return false
    this.follow(id, 50)
    return true
  }

  async start() {
    await exec(["docker", "rm", "-f", this.name], { timeout: 15_000 })
    const argv = ["docker", "run", "-d", "--name", this.name, "--label", `orbit.service=${this.svc.name}`]
    for (const p of this.svc.ports) argv.push("-p", p)
    for (const [k, v] of Object.entries(this.svc.env)) argv.push("-e", `${k}=${v}`)
    for (const v of this.svc.volumes) argv.push("-v", v)
    argv.push(...this.svc.dockerArgs, this.svc.image!)
    if (this.svc.cmd) argv.push(...splitArgs(this.svc.cmd))
    this.cb.log("system", `$ ${argv.join(" ")}`)
    const res = await this.runStreaming(argv, this.svc.cwd)
    const id = res.stdout.trim().split("\n").pop()
    if (res.code !== 0 || !id) throw new Error(`docker run failed (exit ${res.code})`)
    this.follow(id)
  }

  async stop(timeoutMs: number) {
    const target = this.containerId ?? this.name
    await exec(["docker", "stop", "-t", String(Math.ceil(timeoutMs / 1000)), target], { timeout: timeoutMs + 10_000 })
    await exec(["docker", "rm", "-f", target], { timeout: 10_000 })
  }
}

export class ComposeRunner extends ContainerRunner {
  private base(): string[] {
    const argv = ["docker", "compose", "-f", this.svc.composeFile!]
    if (this.svc.composeProject) argv.push("-p", this.svc.composeProject)
    return argv
  }

  private async currentId(): Promise<string | undefined> {
    const res = await exec([...this.base(), "ps", "-q", this.svc.composeService!], { timeout: 10_000 })
    return res.code === 0 ? res.stdout.trim().split("\n")[0] || undefined : undefined
  }

  async attach() {
    const id = await this.currentId()
    if (!id) return false
    const res = await exec(["docker", "inspect", "-f", "{{.State.Running}}", id], { timeout: 5000 })
    if (res.stdout.trim() !== "true") return false
    this.follow(id, 50)
    return true
  }

  async start() {
    const argv = [...this.base(), "up", "-d", "--no-deps", this.svc.composeService!]
    this.cb.log("system", `$ ${argv.join(" ")}`)
    const res = await this.runStreaming(argv, this.svc.cwd)
    if (res.code !== 0) throw new Error(`docker compose up failed (exit ${res.code})`)
    const id = await this.currentId()
    if (!id) throw new Error("container not found after docker compose up")
    this.follow(id)
  }

  async stop(timeoutMs: number) {
    const argv = [...this.base(), "stop", "-t", String(Math.ceil(timeoutMs / 1000)), this.svc.composeService!]
    await exec(argv, { cwd: this.svc.cwd, timeout: timeoutMs + 15_000 })
  }
}
