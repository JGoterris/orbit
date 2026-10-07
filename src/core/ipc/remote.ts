import { EventEmitter } from "node:events"
import type { OrbitConfig, ServiceConfig } from "../../config/schema.ts"
import { dependentsMap, depMapOf, topoOrder } from "../graph.ts"
import { LogStore, type LogLine } from "../logs.ts"
import { isReadyStatus, UP_STATUSES, type ServiceState, type SupervisorLike } from "../supervisor.ts"
import type { ResourceBucket } from "../resources.ts"
import { IpcClient } from "./client.ts"
import { PROTOCOL, type Hello, type Notification } from "./protocol.ts"

/** Everything the daemon has in its log rings. */
const HISTORY_LINES = 20_000

/**
 * A supervisor whose services run elsewhere (an `orbit daemon`, or any orbit with a socket): it mirrors the
 * state and the logs it receives, so the UI can read it synchronously, and forwards every command.
 * Emits "change" like a real Supervisor, and "close" when the connection is lost.
 */
export class RemoteSupervisor extends EventEmitter implements SupervisorLike {
  readonly logs = new LogStore()
  readonly deps
  readonly dependents
  readonly order
  private states = new Map<string, ServiceState>()
  private lastSeq = 0

  /** Connects, checks the protocol and returns a supervisor that has not loaded its state yet (`init()` does). */
  static async connect(path: string): Promise<RemoteSupervisor> {
    const client = await IpcClient.connect(path)
    try {
      const hello = await client.request<Hello>("hello")
      if (hello.protocol !== PROTOCOL) {
        throw new Error(`the running orbit (v${hello.version}) speaks protocol ${hello.protocol}, this one speaks ${PROTOCOL}: run \`orbit down\` and try again`)
      }
      return new RemoteSupervisor(client, hello)
    } catch (err) {
      client.close()
      throw err
    }
  }

  private constructor(
    private client: IpcClient,
    readonly hello: Hello,
  ) {
    super()
    this.setMaxListeners(100)
    this.deps = depMapOf(this.config)
    this.dependents = dependentsMap(this.deps)
    this.order = topoOrder(this.deps)
    for (const name of this.names) this.states.set(name, { name, status: "stopped", restarts: 0, cpu: [], mem: [] })
    client.on("close", () => {
      this.emit("change")
      this.emit("close")
    })
  }

  get config(): OrbitConfig {
    return this.hello.config
  }

  /** Version of the orbit that holds the services. */
  get version() {
    return this.hello.version
  }

  get connected() {
    return !this.client.closed
  }

  /** The daemon's state dir, so exports and the like land next to the logs it writes. */
  get stateDir() {
    return this.hello.stateDir
  }

  get names(): string[] {
    return Object.keys(this.config.services)
  }

  // ---------------------------------------------------------------- queries

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

  history(name: string, sinceMs?: number): Promise<ResourceBucket[]> {
    return this.client.request<ResourceBucket[]>("history", sinceMs ? { service: name, since: sinceMs } : { service: name })
  }

  isReady(name: string): boolean {
    return isReadyStatus(this.state(name).status, this.service(name).oneshot)
  }

  isUp(name: string): boolean {
    const status = this.state(name).status
    return UP_STATUSES.has(status) || status === "waiting"
  }

  isAdopted(name: string): boolean {
    return !!this.state(name).adopted && this.isUp(name)
  }

  runningCount(): number {
    return this.names.filter((n) => this.isUp(n) || this.state(n).status === "stopping").length
  }

  ownedRunningCount(): number {
    return this.names.filter((n) => (this.isUp(n) || this.state(n).status === "stopping") && !this.state(n).adopted).length
  }

  // ---------------------------------------------------------------- lifecycle

  /** Subscribes and loads the current state and the log history. */
  async init(): Promise<void> {
    const buffered: Notification[] = []
    let loading = true
    this.client.on("notification", (n: Notification) => (loading ? buffered.push(n) : this.apply(n)))
    // the snapshot comes with the subscription, so every notification after it is newer than the snapshot
    const sub = await this.client.request<{ snapshot: ServiceState[] }>("subscribe", { states: true, logs: true })
    for (const st of sub.snapshot) this.states.set(st.name, st)
    const history = await this.client.request<LogLine[]>("logs", { limit: HISTORY_LINES })
    for (const line of history) this.ingest(line)
    loading = false
    for (const n of buffered) this.apply(n)
    this.emit("change")
  }

  private ingest(line: LogLine) {
    if (line.seq <= this.lastSeq) return // already in the history
    this.lastSeq = line.seq
    this.logs.ingest(line)
  }

  private apply(n: Notification) {
    if (n.method === "state") {
      const { name, state } = n.params as { name: string; state: ServiceState }
      this.states.set(name, state)
      this.emit("change", name)
    } else if (n.method === "log") this.ingest(n.params as LogLine)
    else if (n.method === "cleared") {
      const { service } = n.params as { service?: string }
      this.logs.clear(service)
      this.emit("change", service)
    }
  }

  // ---------------------------------------------------------------- commands

  private async ok(method: "start" | "stop" | "restart" | "toggle", names: string[]): Promise<boolean> {
    return (await this.client.request<{ ok: boolean }>(method, { services: names })).ok
  }

  start(name: string) {
    return this.ok("start", [name])
  }

  async stop(name: string) {
    await this.ok("stop", [name])
  }

  restart(name: string) {
    return this.ok("restart", [name])
  }

  async toggle(name: string) {
    await this.ok("toggle", [name])
  }

  async startMany(names: string[]) {
    if (names.length) await this.ok("start", names)
  }

  async startAll() {
    await this.client.request("startAll")
  }

  async stopAll() {
    await this.client.request("stopAll")
  }

  toggleWatch(name: string): "paused" | "active" | undefined {
    const watch = this.state(name).watch
    if (!watch) return undefined
    // the daemon's state change that follows updates the mirror
    void this.client.request("toggleWatch", { service: name }).catch(() => {})
    return watch === "paused" ? "active" : "paused"
  }

  clearLogs(name?: string) {
    this.logs.clear(name)
    this.emit("change", name)
    void this.client.request("clearLogs", name ? { service: name } : {}).catch(() => {})
  }

  /** Asks the daemon to stop everything and quit, and waits for it to hang up. */
  async dispose(): Promise<void> {
    if (this.client.closed) return
    const closed = new Promise<void>((resolve) => this.client.once("close", () => resolve()))
    try {
      await this.client.request("shutdown", { how: "stop" })
    } catch {
      return
    }
    await closed
  }

  /** Disconnects; the services keep running under the daemon. */
  detach() {
    this.client.close()
  }

  killAllSync() {
    this.client.close()
  }
}
