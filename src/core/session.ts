import { statSync } from "node:fs"
import { loadConfig } from "../config/load.ts"
import { ConfigError } from "../config/schema.ts"
import { registerProject } from "./projects.ts"
import { acquireLock, releaseLock } from "./state.ts"
import { Supervisor } from "./supervisor.ts"

/** Holds the project that is open and swaps it for another one without restarting the UI. */
export class Session {
  constructor(public sup: Supervisor) {}

  /**
   * Opens the project in `dir`: `how` says what happens to the services of the current one
   * (stop them, or leave them running to be picked up the next time that project is opened).
   * Resolves to an error message when it could not switch; in that case nothing changed.
   */
  async switchTo(dir: string, how: "stop" | "detach", onSwitch?: (sup: Supervisor) => void): Promise<string | undefined> {
    const prev = this.sup
    let sup: Supervisor
    try {
      // allowEmpty would happily turn a typo into a project with no services
      if (!statSync(dir).isDirectory()) return `${dir} is not a folder`
    } catch {
      return `${dir} is not a folder`
    }
    try {
      sup = new Supervisor(loadConfig({ dir, allowEmpty: true }))
    } catch (err) {
      if (err instanceof ConfigError) return err.message
      return (err as Error).message
    }
    if (sup.stateDir === prev.stateDir) return `${sup.config.name} is already open`
    const holder = acquireLock(sup.stateDir)
    if (holder) return `${sup.config.name} is already open in another orbit (pid ${holder})`

    if (how === "detach") prev.detach()
    else await Promise.race([prev.dispose(), Bun.sleep(20_000)])
    releaseLock(prev.stateDir)

    this.sup = sup
    registerProject(sup.config.root, sup.config.name)
    onSwitch?.(sup)
    void sup.init()
  }
}
