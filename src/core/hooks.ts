import type { Subprocess } from "bun"
import type { Hook } from "../config/schema.ts"
import { pipeLines, type LogStream } from "./logs.ts"
import { killTree, shellCommand } from "./platform/index.ts"

export type HookPhase = "pre_start" | "post_start" | "post_stop"

export interface HookContext {
  service: string
  cwd: string
  /** `shell:` of the service */
  shell?: string
  env: Record<string, string>
  /** exit code of the service, for post_stop after it ended on its own */
  exitCode?: number | null
  log(stream: LogStream, text: string): void
  /** the hook process running right now (undefined between hooks), so the owner can kill it */
  track(proc: Subprocess | undefined): void
}

export interface HookResult {
  ok: boolean
  error?: string
}

/** Runs the hooks of one phase in order through the shell (`sh -c`, `cmd /c` on Windows), in the service's cwd; stops at the first failure. */
export async function runHooks(phase: HookPhase, hooks: Hook[], ctx: HookContext): Promise<HookResult> {
  for (const hook of hooks) {
    const started = Date.now()
    ctx.log("system", `▸ ${phase}: ${hook.cmd}`)
    let proc: Subprocess<"ignore", "pipe", "pipe">
    try {
      const sh = shellCommand(hook.cmd, ctx.shell)
      proc = Bun.spawn(sh.argv, {
        windowsVerbatimArguments: sh.verbatim,
        cwd: ctx.cwd,
        env: {
          ...process.env,
          FORCE_COLOR: "1",
          ...ctx.env,
          ORBIT_SERVICE: ctx.service,
          ORBIT_HOOK: phase,
          ...(ctx.exitCode !== undefined && ctx.exitCode !== null ? { ORBIT_EXIT_CODE: String(ctx.exitCode) } : {}),
        },
        stdin: "ignore",
        stdout: "pipe",
        stderr: "pipe",
        // own process group: a timeout or a stop kills the whole tree
        detached: true,
      })
    } catch (err) {
      const error = `${phase} could not start: ${(err as Error).message}`
      ctx.log("system", `✖ ${error}`)
      return { ok: false, error }
    }
    ctx.track(proc)
    let timedOut = false
    const timer = setTimeout(() => {
      timedOut = true
      killTree(proc.pid, "SIGKILL")
    }, hook.timeout)
    const [code] = await Promise.all([
      proc.exited,
      pipeLines(proc.stdout, (line) => ctx.log("stdout", line)),
      pipeLines(proc.stderr, (line) => ctx.log("stderr", line)),
    ])
    clearTimeout(timer)
    ctx.track(undefined)

    if (code !== 0) {
      const why = timedOut ? `timed out after ${Math.round(hook.timeout / 1000)}s` : `failed (exit ${code})`
      const error = `${phase} ${why}: ${hook.cmd}`
      ctx.log("system", `✖ ${error}`)
      return { ok: false, error }
    }
    ctx.log("system", `✓ ${phase} in ${((Date.now() - started) / 1000).toFixed(1)}s`)
  }
  return { ok: true }
}
