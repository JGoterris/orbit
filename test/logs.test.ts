import { describe, expect, test } from "bun:test"
import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { filterLines, formatLines, matcher, readTail, type LogLine } from "../src/core/logs.ts"

const line = (seq: number, service: string, text: string, stream: LogLine["stream"] = "stdout"): LogLine => ({
  seq,
  ts: new Date(2026, 0, 1, 9, 5, 7).getTime(),
  service,
  stream,
  text,
})

describe("log helpers", () => {
  test("matcher: regex, invalid regex falls back to substring, matches the service name", () => {
    const l = line(1, "api", "GET /users 200")
    expect(matcher("")(l)).toBe(true)
    expect(matcher("get .*200")(l)).toBe(true)
    expect(matcher("api")(l)).toBe(true)
    expect(matcher("(")(line(2, "api", "a ( b"))).toBe(true)
    expect(matcher("(")(l)).toBe(false)
    expect(filterLines([l, line(3, "web", "nope")], "users")).toHaveLength(1)
  })

  test("formatLines: plain, with time and with service prefix", () => {
    const ls = [line(1, "api", "hello"), line(2, "db", "boot", "system")]
    expect(formatLines(ls)).toBe("hello\n» boot")
    expect(formatLines(ls, { time: true, prefix: true })).toBe("09:05:07 api │ hello\n09:05:07 db  │ » boot")
  })

  test("readTail: last lines, missing file, cut mid-line", async () => {
    const dir = mkdtempSync(join(tmpdir(), "orbit-tail-"))
    const file = join(dir, "a.log")
    expect(await readTail(file, 5)).toEqual([])
    writeFileSync(file, "one\ntwo\nthree\n")
    expect(await readTail(file, 2)).toEqual(["two", "three"])
    expect(await readTail(file, 0)).toEqual([])
    // only the last 8 bytes are read: "three\n" plus a partial "o\n" that must be dropped
    expect(await readTail(file, 10, 8)).toEqual(["three"])
  })
})
