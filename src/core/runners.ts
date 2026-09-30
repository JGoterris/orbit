import { existsSync } from "node:fs"
import type { Subprocess } from "bun"
import type { ServiceConfig } from "../config/schema.ts"
import { exec, sleep } from "./exec.ts"
import { pipeLines, type LogStream } from "./logs.ts"

export interface RunnerCallbacks {
  log(stream: LogStream, text: string): void
  /** The service stopped (on its own or because we stopped it). */
  exit(code: number | null, signal?: string | null): void
}

export interface Runner {
  /** Launches the service. Resolves once it is launched (not necessarily ready). */
  start(): Promise<void>
  /** Re-attaches to an instance that is already running (containers only). */
  attach?(): Promise<boolean>
  stop(timeoutMs: number): Promise<void>
  /** Synchronous last-resort cleanup on process exit. */
  killSync(): void
  readonly pid?: number
  readonly containerId?: string
}

export function createRunner(svc: ServiceConfig, project: string, cb: RunnerCallbacks): Runner {
  switch (svc.type) {
    case "process":
      return new ProcessRunner(svc, cb)
    case "docker":
      return new DockerRunner(svc, project, cb)
    case "compose":
      return new ComposeRunner(svc, cb)
  }
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

function killGroup(pid: number, signal: NodeJS.Signals): boolean {
  try {
    process.kill(-pid, signal)
    return true
  } catch {
    try {
      process.kill(pid, signal)
      return true
    } catch {
      return false
    }
  }
}

function groupAlive(pid: number): boolean {
  try {
    process.kill(-pid, 0)
    return true
  } catch {
    return false
  }
}

export class ProcessRunner implements Runner {
  private proc?: Subprocess<"ignore", "pipe", "pipe">
  private stopping = false

  constructor(
    private svc: ServiceConfig,
    private cb: RunnerCallbacks,
  ) {}

  get pid() {
    return this.proc?.pid
  }

  async start() {
    if (!existsSync(this.svc.cwd)) throw new Error(`working directory does not exist: ${this.svc.cwd}`)
    this.stopping = false
    const proc = Bun.spawn(["/bin/sh", "-c", this.svc.cmd!], {
      cwd: this.svc.cwd,
      env: { ...process.env, FORCE_COLOR: "1", ...this.svc.env },
      stdin: "ignore",
      stdout: "pipe",
      stderr: "pipe",
      // own process group, so we can signal the whole tree (npm -> node -> esbuild...)
      detached: true,
    })
    this.proc = proc
    const out = pipeLines(proc.stdout, (l) => this.cb.log("stdout", l))
    const err = pipeLines(proc.stderr, (l) => this.cb.log("stderr", l))
    proc.exited.then(async (code) => {
      // leftovers of the group (e.g. a dev server's workers) must not keep ports busy
      if (groupAlive(proc.pid)) killGroup(proc.pid, "SIGTERM")
      await Promise.race([Promise.all([out, err]), sleep(500)])
      if (this.proc === proc) this.cb.exit(code, proc.signalCode)
    })
  }

  killSync() {
    if (this.proc && this.proc.exitCode === null) killGroup(this.proc.pid, "SIGKILL")
  }

  async stop(timeoutMs: number) {
    const proc = this.proc
    if (!proc || proc.exitCode !== null || proc.signalCode !== null) return
    this.stopping = true
    killGroup(proc.pid, "SIGTERM")
    const exited = await Promise.race([proc.exited.then(() => true), sleep(timeoutMs).then(() => false)])
    if (!exited || groupAlive(proc.pid)) {
      this.cb.log("system", exited ? "killing leftover child processes" : `did not stop after ${timeoutMs}ms, sending SIGKILL`)
      killGroup(proc.pid, "SIGKILL")
      await proc.exited
    }
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
  abstract attach(): Promise<boolean>
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
