import { EventEmitter } from "node:events"
import { connect, type Socket } from "node:net"
import { dbg, encode, LineParser, type Method, type Notification, type Response } from "./protocol.ts"

/** Talks to an orbit supervisor over its socket. Emits "notification" (Notification) and "close". */
export class IpcClient extends EventEmitter {
  private socket?: Socket
  private nextId = 1
  private pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: Error) => void }>()
  closed = false

  /** Resolves a connected client, or rejects when nobody is listening. */
  static connect(path: string, timeoutMs = 2000): Promise<IpcClient> {
    return new Promise((resolve, reject) => {
      const client = new IpcClient()
      const socket = connect(path)
      const timer = setTimeout(() => socket.destroy(new Error("timeout connecting to orbit")), timeoutMs)
      socket.once("connect", () => {
        clearTimeout(timer)
        client.attach(socket)
        resolve(client)
      })
      socket.once("error", (err) => {
        clearTimeout(timer)
        reject(err)
      })
    })
  }

  private attach(socket: Socket) {
    this.socket = socket
    socket.setEncoding("utf8")
    const parser = new LineParser((msg) => {
      dbg("client recv", msg.id ?? msg.method)
      if (typeof msg.id === "number" && ("result" in msg || "error" in msg)) {
        const p = this.pending.get(msg.id)
        if (!p) return
        this.pending.delete(msg.id)
        const res = msg as unknown as Response
        if (res.error) p.reject(new Error(res.error.message))
        else p.resolve(res.result)
      } else if (typeof msg.method === "string") this.emit("notification", msg as unknown as Notification)
    })
    socket.on("data", (chunk) => {
      dbg("client data", (chunk as string).length, "bytes")
      parser.push(chunk as string)
    })
    socket.on("error", (err) => dbg("client socket error", err.message))
    socket.on("close", () => {
      dbg("client socket close", this.pending.size, "pending")
      this.closed = true
      for (const p of this.pending.values()) p.reject(new Error("connection to orbit closed"))
      this.pending.clear()
      this.emit("close")
    })
  }

  request<T = unknown>(method: Method, params?: Record<string, unknown>): Promise<T> {
    if (this.closed || !this.socket) return Promise.reject(new Error("not connected to orbit"))
    const id = this.nextId++
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject })
      dbg("client send", id, method)
      this.socket!.write(encode({ jsonrpc: "2.0", id, method, params }))
    })
  }

  close() {
    this.socket?.destroy()
  }
}
