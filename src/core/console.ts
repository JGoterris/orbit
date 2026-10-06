import { EventEmitter } from "node:events"
import type { Subprocess } from "bun"
import { readEnvFiles } from "../config/envFiles.ts"
import type { ServiceConfig } from "../config/schema.ts"
import { killGroup } from "./runners.ts"

export interface ConsoleSpec {
  argv: string[]
  cwd?: string
  env: Record<string, string>
  /** what the modal shows in its title */
  title: string
}

const BACKLOG_BYTES = 2 * 1024 * 1024
const CONTAINER_SHELL = "command -v bash >/dev/null && exec bash || exec sh"

/**
 * What `i` runs for a service. A process (or an external service, on the host) gets its `console:` command (or $SHELL) in its own cwd and env;
 * a container gets `docker exec -it`, so it has to be running.
 */
export function consoleCommand(
  svc: ServiceConfig,
  state: { status: string; containerId?: string },
): ConsoleSpec | { error: string } {
  if (svc.type === "process" || svc.type === "external") {
    let env: Record<string, string>
    try {
      env = { ...readEnvFiles(svc.envFiles), ...svc.env }
    } catch (err) {
      return { error: (err as Error).message }
    }
    const shell = process.env.SHELL || "/bin/sh"
    return {
      argv: svc.console ? ["/bin/sh", "-c", svc.console] : [shell],
      cwd: svc.cwd,
      env: { ...env, ORBIT_SERVICE: svc.name },
      title: svc.console ?? shell,
    }
  }
  if (!state.containerId || !["running", "healthy", "unhealthy"].includes(state.status)) {
    return { error: `${svc.name} is not running` }
  }
  return {
    argv: ["docker", "exec", "-it", state.containerId, "sh", "-c", svc.console ?? CONTAINER_SHELL],
    env: {},
    title: `docker exec ${svc.console ?? "shell"}`,
  }
}

/**
 * Bun hands the program a pty but not as its controlling terminal, so an interactive bash complains
 * ("cannot set terminal process group", "no job control") and ctrl+c / ctrl+z do not work. `setsid -c` fixes it.
 */
function withControllingTty(argv: string[]): string[] {
  const setsid = process.platform === "linux" ? Bun.which("setsid") : null
  return setsid ? [setsid, "-c", ...argv] : argv
}

/** An interactive program on a PTY. Keeps the raw output so a modal opened later can repaint the screen. */
export class ConsoleSession extends EventEmitter {
  private chunks: Uint8Array[] = []
  private size = 0
  private proc?: Subprocess
  private terminal?: Bun.Terminal
  exitCode: number | null | undefined
  readonly exited: Promise<void>

  constructor(
    readonly spec: ConsoleSpec,
    cols: number,
    rows: number,
  ) {
    super()
    this.terminal = new Bun.Terminal({
      cols,
      rows,
      name: "xterm-256color",
      data: (_t, data) => {
        const copy = data.slice()
        this.chunks.push(copy)
        this.size += copy.length
        while (this.size > BACKLOG_BYTES && this.chunks.length > 1) this.size -= this.chunks.shift()!.length
        this.emit("data", copy)
      },
    })
    try {
      this.proc = Bun.spawn(withControllingTty(spec.argv), {
        cwd: spec.cwd,
        env: { ...process.env, ...spec.env, TERM: "xterm-256color" },
        terminal: this.terminal,
      })
    } catch (err) {
      this.terminal.close()
      throw err
    }
    this.exited = this.proc.exited.then((code) => {
      this.exitCode = code
      this.terminal?.close()
      this.emit("exit", code)
    })
  }

  get alive() {
    return this.exitCode === undefined
  }

  /** everything the program has printed (bounded), to replay into a fresh emulator */
  get backlog(): Uint8Array {
    const out = new Uint8Array(this.size)
    let at = 0
    for (const c of this.chunks) {
      out.set(c, at)
      at += c.length
    }
    return out
  }

  write(data: string | Uint8Array) {
    if (this.alive && !this.terminal?.closed) this.terminal?.write(data)
  }

  resize(cols: number, rows: number) {
    if (this.alive && !this.terminal?.closed) this.terminal?.resize(cols, rows)
  }

  kill() {
    if (this.proc && this.alive) {
      if (!killGroup(this.proc.pid, "SIGHUP")) this.proc.kill("SIGHUP")
      setTimeout(() => this.alive && this.proc?.kill("SIGKILL"), 1000).unref()
    }
  }
}

/** One live console per service: closing the modal keeps it, `i` brings it back. */
export class ConsoleManager {
  private sessions = new Map<string, ConsoleSession>()

  constructor() {
    // quitting orbit must not leave a psql behind
    process.once("exit", () => this.closeAll())
  }

  has(name: string) {
    return this.sessions.get(name)?.alive ?? false
  }

  get(name: string) {
    return this.sessions.get(name)
  }

  /** the live session of the service, or a new one */
  open(name: string, spec: ConsoleSpec, cols: number, rows: number): ConsoleSession {
    const cur = this.sessions.get(name)
    if (cur?.alive) return cur
    const session = new ConsoleSession(spec, cols, rows)
    this.sessions.set(name, session)
    return session
  }

  /** forgets a finished session (its modal was dismissed) */
  discard(name: string) {
    const s = this.sessions.get(name)
    if (s && !s.alive) this.sessions.delete(name)
  }

  closeAll() {
    for (const s of this.sessions.values()) s.kill()
    this.sessions.clear()
  }
}
