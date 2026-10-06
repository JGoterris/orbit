import { describe, expect, test } from "bun:test"
import { foldTraces, LogStore, withFrames } from "../src/core/logs.ts"

const JAVA = [
  'Exception in thread "main" java.lang.IllegalStateException: boom',
  "\tat com.acme.Service.run(Service.java:42)",
  "\tat com.acme.Main.main(Main.java:10)",
  "Caused by: java.io.IOException: disk",
  "\tat com.acme.Io.read(Io.java:7)",
  "\t... 5 more",
]
const NODE = [
  "TypeError: Cannot read properties of null (reading 'x')",
  "    at Object.<anonymous> (/app/index.js:1:6)",
  "    at async Promise.all (index 0)",
  "    at node:internal/main:1:1",
]
const PY = [
  "Traceback (most recent call last):",
  '  File "a.py", line 3, in <module>',
  "    1/0",
  "ZeroDivisionError: division by zero",
]

function feed(store: LogStore, lines: string[], service = "api", stream: "stdout" | "stderr" = "stderr") {
  for (const l of lines) store.append(service, stream, l)
}

describe("stack trace detection", () => {
  test("java: head is the line before the first frame, Caused by and ... N more belong to it", () => {
    const s = new LogStore()
    feed(s, ["starting", ...JAVA, "after"])
    const ls = s.lines()
    expect(ls[1]!.trace).toEqual({ kind: "java", frames: 5 })
    expect(ls.slice(2, 7).every((l) => l.traceOf === ls[1]!.seq)).toBe(true)
    expect(ls[0]!.trace).toBeUndefined()
    expect(ls[7]!.traceOf).toBeUndefined()
  })

  test("node: at async and file frames", () => {
    const s = new LogStore()
    feed(s, NODE)
    expect(s.lines()[0]!.trace).toEqual({ kind: "node", frames: 3 })
  })

  test("python: summary and chained tracebacks stay in one group", () => {
    const s = new LogStore()
    feed(s, [...PY, "", "During handling of the above exception, another exception occurred:", "", ...PY, "next"])
    const ls = s.lines()
    expect(ls[0]!.trace!.kind).toBe("python")
    expect(ls[0]!.trace!.summary).toBe("ZeroDivisionError: division by zero")
    expect(ls.slice(1, 11).every((l) => l.traceOf === ls[0]!.seq)).toBe(true)
    expect(ls[11]!.traceOf).toBeUndefined()
  })

  test("interleaved services do not mix", () => {
    const s = new LogStore()
    for (let i = 0; i < NODE.length; i++) {
      s.append("a", "stderr", NODE[i]!)
      s.append("b", "stderr", `noise ${i}`)
    }
    const ls = s.lines()
    expect(ls[0]!.trace?.frames).toBe(3)
    expect(ls.filter((l) => l.service === "b").every((l) => !l.trace && l.traceOf === undefined)).toBe(true)
  })

  test("fold hides frames, expanded shows them, a missing head keeps them visible", () => {
    const s = new LogStore()
    feed(s, ["x", ...NODE, "y"])
    const ls = s.lines()
    expect(foldTraces(ls, new Set()).map((l) => l.text)).toEqual(["x", NODE[0]!, "y"])
    expect(foldTraces(ls, new Set([ls[1]!.seq]))).toHaveLength(ls.length)
    expect(foldTraces(ls.slice(2), new Set())).toHaveLength(ls.length - 2)
  })

  test("a one-frame trace is not folded", () => {
    const s = new LogStore()
    feed(s, ["Error: x", "    at foo (a.js:1:1)", "done"])
    expect(foldTraces(s.lines(), new Set())).toHaveLength(3)
  })

  test("withFrames restores the hidden lines of a copied head", () => {
    const s = new LogStore()
    feed(s, [...PY, "z"])
    const all = s.lines()
    const folded = foldTraces(all, new Set())
    expect(withFrames(folded, all).map((l) => l.text)).toEqual([...PY, "z"])
  })

  test("ingest ignores the sender's trace tags and detects again", () => {
    const s = new LogStore()
    const src = new LogStore()
    feed(src, NODE)
    for (const l of src.lines()) s.ingest({ ...l, seq: 999 })
    const ls = s.lines()
    expect(ls[0]!.trace?.frames).toBe(3)
    expect(ls[1]!.traceOf).toBe(ls[0]!.seq)
  })
})
