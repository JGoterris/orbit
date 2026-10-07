import { describe, expect, test } from "bun:test"
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import YAML from "yaml"
import schema from "../orbit.schema.json"
import { unknownKeys, validateFile } from "../src/config/validate.ts"

function project(yaml: string): string {
  const dir = mkdtempSync(join(tmpdir(), "orbit-validate-"))
  writeFileSync(join(dir, "orbit.yaml"), yaml)
  return dir
}

/** Every property name the schema defines, at any depth */
function schemaKeys(node: unknown, out = new Set<string>()): Set<string> {
  if (Array.isArray(node)) node.forEach((n) => schemaKeys(n, out))
  else if (node && typeof node === "object") {
    for (const [k, v] of Object.entries(node)) {
      if (k === "properties") Object.keys(v as object).forEach((p) => out.add(p))
      schemaKeys(v, out)
    }
  }
  return out
}

describe("orbit.schema.json", () => {
  test("every $ref resolves", () => {
    const refs = [...JSON.stringify(schema).matchAll(/"\$ref":\s*"#\/definitions\/([A-Za-z]+)"/g)].map((m) => m[1]!)
    expect(refs.length).toBeGreaterThan(10)
    for (const r of refs) expect((schema.definitions as Record<string, unknown>)[r], r).toBeDefined()
  })

  test("covers every key the loader reads", () => {
    const known = schemaKeys(schema)
    const src = ["../src/config/load.ts", "../src/config/envFiles.ts"].map((f) => readFileSync(new URL(f, import.meta.url), "utf8")).join("\n")
    const read = new Set([...src.matchAll(/\b(?:rec|doc)\.([A-Za-z_]+)/g)].map((m) => m[1]!))
    expect(read.size).toBeGreaterThan(30)
    expect([...read].filter((k) => !known.has(k))).toEqual([])
  })

  test("the example in the README has no unknown keys", () => {
    const readme = readFileSync(new URL("../README.md", import.meta.url), "utf8")
    const block = /```yaml\n(name: my-stack[\s\S]*?)```/.exec(readme)?.[1]
    expect(block).toBeDefined()
    expect(unknownKeys(YAML.parse(block!))).toEqual([])
  })

  test("the example stack fixture is valid", () => {
    const res = validateFile({ dir: new URL("./fixtures/stack", import.meta.url).pathname })
    expect(res.errors).toEqual([])
  })
})

describe("unknownKeys", () => {
  test("typos are reported with a suggestion, at any level", () => {
    const doc = YAML.parse(`
nmae: x
services:
  api:
    cmd: run
    helth: "tcp:1"
    depend_on: [db]
    watch: { paths: [a], debonce: 1s }
    health: { http: "http://x", intervall: 1s }
    pre_start: [{ cmd: x, timout: 1s }]
    ready_when: { logg: x }
groups: { g: [api] }
`)
    expect(unknownKeys(doc)).toEqual([
      { path: "", key: "nmae", suggestion: "name" },
      { path: "services.api", key: "helth", suggestion: "health" },
      { path: "services.api", key: "depend_on", suggestion: "depends_on" },
      { path: "services.api.watch", key: "debonce", suggestion: "debounce" },
      { path: "services.api.health", key: "intervall", suggestion: "interval" },
      { path: "services.api.pre_start[0]", key: "timout", suggestion: "timeout" },
      { path: "services.api.ready_when", key: "logg", suggestion: "log" },
    ])
  })

  test("valid forms are accepted: shortcuts, aliases, free-form env and compose overrides", () => {
    const doc = YAML.parse(`
compose: false
env: { A: 1, B: x }
services:
  a: bun run dev
  b: { command: x, environment: [K=v], dependsOn: a, env_file: [.env, { path: .x, required: false }], health: false, watch: ["src/**"] }
  c: { image: redis, ports: ["1:1", 2], volumes: [./d:/d] }
  d: { type: external, health: "tcp:db:5432" }
  e:
`)
    expect(unknownKeys(doc)).toEqual([])
  })
})

describe("validateFile", () => {
  test("returns loader errors instead of throwing", () => {
    const res = validateFile({ dir: project("services:\n  a: { cmd: x, restart: sometimes }\n") })
    expect(res.errors).toHaveLength(1)
    expect(res.errors[0]).toContain("restart")
  })

  test("reports unknown keys the loader silently ignores", () => {
    const res = validateFile({ dir: project("services:\n  a: { cmd: x, helth: 'tcp:1' }\n") })
    expect(res.config).toBeDefined()
    expect(res.errors).toEqual(['services.a.helth: unknown key (did you mean "health"?)'])
  })

  test("a valid file has no errors", () => {
    const res = validateFile({ dir: project("services:\n  a: { cmd: x, port: 3000 }\ngroups: { all: [a] }\n") })
    expect(res.errors).toEqual([])
    expect(Object.keys(res.config!.services)).toEqual(["a"])
  })
})
