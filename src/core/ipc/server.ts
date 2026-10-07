import { chmodSync, mkdirSync, rmSync } from "node:fs"
import { createServer, type Server, type Socket } from "node:net"
import pkg from "../../../package.json"
import type { ConfigDiff } from "../../config/diff.ts"
import type { OrbitConfig } from "../../config/schema.ts"
import type { LogLine } from "../logs.ts"
import type { PendingConfig, SupervisorLike } from "../supervisor.ts"
import { IpcClient } from "./client.ts"
import { socketDir, socketPath } from "./endpoint.ts"
import { encode, ERR, LineParser, PROTOCOL, type Hello, type Method, type Notification } from "./protocol.ts"

/** A client that lets this much output pile up unread is cut off instead of growing our memory. */
const MAX_BACKLOG = 4 * 1024 * 1024

interface Subscription {
  states: boolean
  logs: boolean
  services?: Set<string>
}

class RpcError extends Error {
  constructor(
    readonly code: number,
    message: string,
  ) {
    super(message)
  }
}

/** Exposes a supervisor on a local socket: queries, commands and a stream of state / log notifications. */
export class IpcServer {
  readonly path: string
  private server?: Server
  private clients = new Map<Socket, Subscription>()
  private offLine?: () => void
  private onChange = (name?: string) => {
    if (name) this.broadcast("state", (sub) => sub.states && this.wants(sub, name), { name, state: this.sup.state(name) })
  }

  private onConfig = (diff: ConfigDiff) => {
    this.broadcast("config", (sub) => sub.states, { config: this.sup.config, diff })
    this.broadcast("configPending", (sub) => sub.states, { pending: null })
  }
  private onPending = (pending?: PendingConfig) => this.broadcast("configPending", (sub) => sub.states, { pending: pending ?? null })

  constructor(
    private sup: SupervisorLike,
    private opts: { onShutdown?: (how: "stop" | "detach") => void } = {},
  ) {
    this.path = socketPath(sup.stateDir)
  }

  /** Starts listening. False when another orbit already answers on this socket. */
  async start(): Promise<boolean> {
    const live = await IpcClient.connect(this.path, 500).then(
      (c) => (c.close(), true),
      () => false,
    )
    if (live) return false
    const dir = socketDir(this.path)
    if (dir) {
      mkdirSync(dir, { recursive: true, mode: 0o700 })
      rmSync(this.path, { force: true }) // leftover of a process that did not clean up
    }
    const server = createServer((socket) => this.accept(socket))
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject)
      server.listen(this.path, () => {
        server.off("error", reject)
        resolve()
      })
    })
    server.on("error", () => {})
    if (dir) chmodSync(this.path, 0o600)
    this.server = server
    this.sup.on("change", this.onChange)
    this.sup.on("config", this.onConfig)
    this.sup.on("configPending", this.onPending)
    this.offLine = this.sup.logs.onLine((line) => this.broadcast("log", (sub) => sub.logs && this.wants(sub, line.service), line))
    return true
  }

  close() {
    this.sup.off("change", this.onChange)
    this.sup.off("config", this.onConfig)
    this.sup.off("configPending", this.onPending)
    this.offLine?.()
    for (const socket of this.clients.keys()) socket.destroy()
    this.clients.clear()
    this.server?.close()
    this.server = undefined
    if (socketDir(this.path)) rmSync(this.path, { force: true })
  }

  get clientCount() {
    return this.clients.size
  }

  private wants(sub: Subscription, service: string) {
    return !sub.services || sub.services.has(service)
  }

  private broadcast(method: Notification["method"], pick: (sub: Subscription) => boolean, params: Notification["params"]) {
    let line: string | undefined
    for (const [socket, sub] of this.clients) {
      if (!pick(sub)) continue
      if (socket.writableLength > MAX_BACKLOG) {
        socket.destroy()
        continue
      }
      socket.write((line ??= encode({ jsonrpc: "2.0", method, params })))
    }
  }

  private accept(socket: Socket) {
    this.clients.set(socket, { states: false, logs: false })
    socket.setEncoding("utf8")
    const send = (msg: Parameters<typeof encode>[0]) => socket.writable && socket.write(encode(msg))
    const parser = new LineParser(
      (msg) => {
        const id = typeof msg.id === "number" ? msg.id : null
        if (typeof msg.method !== "string") return void send({ jsonrpc: "2.0", id, error: { code: ERR.invalid, message: "invalid request" } })
        const params = (msg.params && typeof msg.params === "object" ? msg.params : {}) as Record<string, unknown>
        this.handle(socket, msg.method as Method, params).then(
          (result) => id !== null && send({ jsonrpc: "2.0", id, result }),
          (err) => {
            const code = err instanceof RpcError ? err.code : ERR.internal
            send({ jsonrpc: "2.0", id, error: { code, message: (err as Error).message } })
          },
        )
      },
      () => send({ jsonrpc: "2.0", id: null, error: { code: ERR.parse, message: "parse error" } }),
    )
    socket.on("data", (chunk) => parser.push(chunk as string))
    socket.on("error", () => {})
    socket.on("close", () => this.clients.delete(socket))
  }

  /** Service names from params; groups expand to their members. */
  private targets(params: Record<string, unknown>): string[] {
    const raw = params.services
    if (!Array.isArray(raw) || raw.some((n) => typeof n !== "string")) throw new RpcError(ERR.params, "services must be a list of names")
    const out = new Set<string>()
    for (const n of raw as string[]) {
      const members = this.sup.config.groups[n] ?? (this.sup.config.services[n] ? [n] : undefined)
      if (!members) throw new RpcError(ERR.params, `unknown service or group "${n}"`)
      members.forEach((m) => out.add(m))
    }
    return [...out]
  }

  private service(params: Record<string, unknown>): string {
    const name = params.service
    if (typeof name !== "string" || !this.sup.config.services[name]) throw new RpcError(ERR.params, `unknown service "${String(name)}"`)
    return name
  }

  private async handle(socket: Socket, method: Method, params: Record<string, unknown>): Promise<unknown> {
    const sup = this.sup
    switch (method) {
      case "hello":
        return { protocol: PROTOCOL, version: pkg.version, pid: process.pid, stateDir: sup.stateDir, config: sup.config, pending: sup.pendingConfig() } satisfies Hello
      case "snapshot":
        return sup.snapshot()
      case "logs": {
        const lines: readonly LogLine[] = sup.logs.lines(params.service === undefined ? undefined : this.service(params))
        const since = typeof params.sinceSeq === "number" ? params.sinceSeq : 0
        const limit = typeof params.limit === "number" && params.limit >= 0 ? params.limit : 200
        const fresh = since ? lines.filter((l) => l.seq > since) : lines
        return limit ? fresh.slice(-limit) : []
      }
      case "start":
      case "stop":
      case "restart":
      case "toggle": {
        const names = this.targets(params)
        if (method === "start") return { ok: (await Promise.all(names.map((n) => sup.start(n)))).every(Boolean) }
        if (method === "stop") await Promise.all(names.map((n) => sup.stop(n)))
        else if (method === "restart") return { ok: (await Promise.all(names.map((n) => sup.restart(n)))).every(Boolean) }
        else await Promise.all(names.map((n) => sup.toggle(n)))
        return { ok: true }
      }
      case "startAll":
        await sup.startAll()
        return { ok: true }
      case "stopAll":
        await sup.stopAll()
        return { ok: true }
      case "toggleWatch":
        return { watch: sup.toggleWatch(this.service(params)) ?? null }
      case "clearLogs": {
        const service = params.service === undefined ? undefined : this.service(params)
        sup.clearLogs(service)
        this.broadcast("cleared", () => true, { service })
        return { ok: true }
      }
      case "history":
        return sup.history(this.service(params), typeof params.since === "number" && params.since > 0 ? params.since : undefined)
      case "reload": {
        const given = params.config as OrbitConfig | undefined
        if (given !== undefined && (typeof given !== "object" || given === null || typeof given.services !== "object")) throw new RpcError(ERR.params, "config must be an orbit config")
        try {
          return { ok: true, diff: await sup.reload(given) }
        } catch (err) {
          throw new RpcError(ERR.params, (err as Error).message) // a config that does not load: nothing was touched
        }
      }
      case "subscribe": {
        const services = Array.isArray(params.services) ? new Set(this.targets(params)) : undefined
        this.clients.set(socket, { states: params.states !== false, logs: !!params.logs, services })
        return { ok: true, snapshot: sup.snapshot() }
      }
      case "shutdown": {
        const how = params.how === "detach" ? "detach" : "stop"
        // answer first: the caller is waiting for the reply while we tear everything down
        setTimeout(() => this.opts.onShutdown?.(how), 10)
        return { ok: true }
      }
      default:
        throw new RpcError(ERR.method, `unknown method "${String(method)}"`)
    }
  }
}
