import { statSync } from "node:fs"
import { loadConfig } from "../config/load.ts"
import { ConfigError } from "../config/schema.ts"
import { registerProject } from "./projects.ts"
import { connectRemote } from "./ipc/daemon.ts"
import { RemoteSupervisor } from "./ipc/remote.ts"
import { IpcServer } from "./ipc/server.ts"
import { acquireLock, releaseLock, stateDir } from "./state.ts"
import { Supervisor, type SupervisorLike } from "./supervisor.ts"

/** Holds the project that is open and swaps it for another one without restarting the UI. */
export class Session {
  private ipc?: IpcServer
  private switching = false

  /**
   * `onShutdown` runs when a client asks over the socket to quit orbit (`orbit down`).
   * `daemon`: projects opened from here run in an `orbit daemon` (started if needed) and this process only
   * mirrors them; without it they run in this process.
   * `onDisconnect` runs when the daemon of the open project goes away on its own (`orbit down`, a crash).
   */
  constructor(
    public sup: SupervisorLike,
    private opts: { onShutdown?: (how: "stop" | "detach") => void; onDisconnect?: () => void; daemon?: boolean } = {},
  ) {
    this.watch(sup)
  }

  private watch(sup: SupervisorLike) {
    // a remote supervisor emits "close" when its connection ends; leaving the project on purpose does not count
    sup.on("close", () => {
      if (this.sup === sup && !this.switching) this.opts.onDisconnect?.()
    })
  }

  /** Exposes the open project on its socket. Best effort: orbit works the same without it. */
  async serve(): Promise<void> {
    this.closeIpc()
    if (this.sup instanceof RemoteSupervisor) return // its daemon already serves it
    try {
      const ipc = new IpcServer(this.sup, { onShutdown: this.opts.onShutdown })
      if (await ipc.start()) this.ipc = ipc
    } catch {}
  }

  closeIpc() {
    this.ipc?.close()
    this.ipc = undefined
  }

  /**
   * Opens the project in `dir`: `how` says what happens to the services of the current one
   * (stop them, or leave them running to be picked up the next time that project is opened).
   * Resolves to an error message when it could not switch; in that case nothing changed.
   */
  async switchTo(dir: string, how: "stop" | "detach", onSwitch?: (sup: SupervisorLike) => void): Promise<string | undefined> {
    const prev = this.sup
    let next: SupervisorLike
    let local = false
    try {
      // allowEmpty would happily turn a typo into a project with no services
      if (!statSync(dir).isDirectory()) return `${dir} is not a folder`
    } catch {
      return `${dir} is not a folder`
    }
    try {
      const config = loadConfig({ dir, allowEmpty: true })
      if (stateDir(config) === prev.stateDir) return `${config.name} is already open`
      if (this.opts.daemon && Object.keys(config.services).length) {
        next = await connectRemote(config)
      } else {
        const sup = new Supervisor(config)
        const holder = acquireLock(sup.stateDir)
        if (holder) return `${sup.config.name} is already open in another orbit (pid ${holder})`
        next = sup
        local = true
      }
    } catch (err) {
      return (err as Error).message // ConfigError or a daemon that would not start
    }

    this.closeIpc()
    this.switching = true
    try {
      if (how === "detach") prev.detach()
      else await Promise.race([prev.dispose(), Bun.sleep(20_000)])
    } finally {
      this.switching = false
    }
    releaseLock(prev.stateDir)

    this.watch(next)
    this.sup = next
    registerProject(next.config.root, next.config.name)
    onSwitch?.(next)
    if (local) void this.serve()
    void next.init().catch(() => {})
  }
}
