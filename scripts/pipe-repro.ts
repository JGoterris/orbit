// Temporary: reproduces "a response written by the server never reaches the client" on Windows named pipes.
// Each variant: 3 sequential requests over a fresh pipe; the 3rd is answered in a different way. Prints OK / HANG.
import { createServer, connect, type Socket } from "node:net"
import { tmpdir } from "node:os"
import { join } from "node:path"

const win = process.platform === "win32"
let n = 0
const endpoint = () => (win ? `\\\\.\\pipe\\orbit-repro-${process.pid}-${n++}` : join(tmpdir(), `orbit-repro-${process.pid}-${n++}.sock`))

type Reply = (socket: Socket, id: number) => void | Promise<void>
const line = (o: unknown) => JSON.stringify(o) + "\n"

const variants: Record<string, { third: Reply; encoding?: boolean }> = {
  "A: error via rejected promise (like orbit)": {
    third: (s, id) => Promise.reject(new Error('unknown service "nope"')).then(() => {}, (e) => void s.write(line({ id, error: { message: e.message } }))),
  },
  "B: same, error text without quotes": {
    third: (s, id) => Promise.reject(new Error("unknown service nope")).then(() => {}, (e) => void s.write(line({ id, error: { message: e.message } }))),
  },
  "C: plain result written synchronously": { third: (s, id) => void s.write(line({ id, result: [] })) },
  "D: error written inside setImmediate": { third: (s, id) => void setImmediate(() => s.write(line({ id, error: { message: "x" } }))) },
  "E: error written after 20 ms": { third: (s, id) => void setTimeout(() => s.write(line({ id, error: { message: "x" } })), 20) },
  "F: error written with a callback + cork/uncork": { third: (s, id) => { s.cork(); s.write(line({ id, error: { message: "x" } })); s.uncork() } },
  "G: error answered like orbit but 3rd request comes 50 ms later": { third: (s, id) => void s.write(line({ id, error: { message: "x" } })) },
}

async function run(name: string, v: (typeof variants)[string]) {
  const path = endpoint()
  const server = createServer((socket) => {
    socket.setEncoding("utf8")
    let buf = ""
    socket.on("data", (chunk) => {
      buf += chunk
      let nl: number
      while ((nl = buf.indexOf("\n")) !== -1) {
        const msg = JSON.parse(buf.slice(0, nl))
        buf = buf.slice(nl + 1)
        if (msg.id === 3) void v.third(socket, msg.id)
        else void Promise.resolve([]).then((result) => socket.write(line({ id: msg.id, result })))
      }
    })
    socket.on("error", () => {})
  })
  await new Promise<void>((r) => server.listen(path, r))
  const client = connect(path)
  await new Promise<void>((r) => client.once("connect", r))
  client.setEncoding("utf8")
  const waiting = new Map<number, () => void>()
  let buf = ""
  client.on("data", (chunk) => {
    buf += chunk
    let nl: number
    while ((nl = buf.indexOf("\n")) !== -1) {
      const msg = JSON.parse(buf.slice(0, nl))
      buf = buf.slice(nl + 1)
      waiting.get(msg.id)?.()
    }
  })
  const request = (id: number) =>
    Promise.race([
      new Promise<boolean>((r) => {
        waiting.set(id, () => r(true))
        client.write(line({ id, method: "history" }))
      }),
      new Promise<boolean>((r) => setTimeout(() => r(false), 1500)),
    ])
  const results = [await request(1), await request(2)]
  if (name.startsWith("G")) await Bun.sleep(50)
  results.push(await request(3))
  console.log(results.every(Boolean) ? "OK  " : "HANG", name, JSON.stringify(results))
  client.destroy()
  server.close()
}

for (const [name, v] of Object.entries(variants)) await run(name, v)
process.exit(0)
