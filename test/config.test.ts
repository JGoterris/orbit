import { describe, expect, test } from "bun:test"
import { mkdtempSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadConfig, parseDotEnv, interpolate } from "../src/config/load.ts"
import { parseDuration, hostPortOf } from "../src/config/schema.ts"
import { splitArgs } from "../src/core/runners.ts"
import { readEnvFiles } from "../src/config/envFiles.ts"

function project(files: Record<string, string>) {
  const dir = mkdtempSync(join(tmpdir(), "orbit-test-"))
  for (const [name, content] of Object.entries(files)) writeFileSync(join(dir, name), content)
  return dir
}

describe("config", () => {
  test("durations and ports", () => {
    expect(parseDuration("2s", "x", 0)).toBe(2000)
    expect(parseDuration("150ms", "x", 0)).toBe(150)
    expect(parseDuration(undefined, "x", 7)).toBe(7)
    expect(() => parseDuration("soon", "x", 0)).toThrow()
    expect(hostPortOf("8080:80")).toBe(8080)
    expect(hostPortOf("127.0.0.1:5432:5432/tcp")).toBe(5432)
    expect(hostPortOf("3000")).toBe(3000)
  })

  test("env interpolation and .env", () => {
    expect(parseDotEnv("A=1\n# c\nexport B='two'\nC=3 # note")).toEqual({ A: "1", B: "two", C: "3" })
    expect(interpolate({ a: "${X}-${Y:-def}" }, { X: "x" })).toEqual({ a: "x-def" })
  })

  test("splitArgs", () => {
    expect(splitArgs(`redis-server --save "" --appendonly 'yes no'`)).toEqual(["redis-server", "--save", "", "--appendonly", "yes no"])
  })

  test("loads services, infers types and merges compose", () => {
    const dir = project({
      "docker-compose.yml": `
services:
  postgres:
    image: postgres:16
    ports: ["5433:5432"]
    healthcheck: { test: ["CMD", "pg_isready"] }
  mailer:
    image: mailhog/mailhog
    depends_on:
      postgres: { condition: service_healthy }
`,
      ".env": "API_PORT=4000",
      "orbit.yaml": `
name: demo
services:
  api:
    cmd: bun run dev
    port: \${API_PORT}
    depends_on: [postgres, redis]
    restart: on-failure
  redis:
    image: redis:7
    ports: ["6379:6379"]
  postgres:
    restart: always
groups:
  backend: [api, postgres]
`,
    })
    const c = loadConfig({ dir, env: {} })
    expect(c.name).toBe("demo")
    expect(Object.keys(c.services).sort()).toEqual(["api", "mailer", "postgres", "redis"])
    expect(c.services.api!.type).toBe("process")
    expect(c.services.api!.port).toBe(4000)
    expect(c.services.api!.health?.tcp).toBe("4000")
    expect(c.services.redis!.type).toBe("docker")
    expect(c.services.postgres!.type).toBe("compose")
    expect(c.services.postgres!.restart).toBe("always")
    expect(c.services.postgres!.port).toBe(5433)
    expect(c.services.postgres!.health?.container).toBe(true)
    expect(c.services.mailer!.dependsOn).toEqual(["postgres"])
    expect(c.groups.backend).toEqual(["api", "postgres"])
  })

  test("rejects unknown deps and cycles", () => {
    expect(() => loadConfig({ dir: project({ "orbit.yaml": "services:\n  a: { cmd: x, depends_on: [b] }" }) })).toThrow(/unknown service "b"/)
    expect(() =>
      loadConfig({ dir: project({ "orbit.yaml": "services:\n  a: { cmd: x, depends_on: [b] }\n  b: { cmd: y, depends_on: [a] }" }) }),
    ).toThrow(/cycle/)
    expect(() => loadConfig({ dir: project({ "orbit.yaml": "services:\n  a: { port: 1 }" }) })).toThrow(/needs `cmd`/)
  })

  test("compose-only project", () => {
    const c = loadConfig({ dir: project({ "compose.yaml": "services:\n  web: { image: nginx, ports: ['8081:80'] }" }) })
    expect(c.services.web!.type).toBe("compose")
  })
})

test("compose files get ${VAR:-default} interpolation", () => {
  const c = loadConfig({
    dir: project({ "compose.yaml": "services:\n  gw:\n    image: x\n    ports: ['${GW_PORT:-8280}:8280']" }),
    env: {},
  })
  expect(c.services.gw!.port).toBe(8280)
})

describe("env_file", () => {
  test("compose files get ${VAR:-default} interpolation", () => {
    const c = loadConfig({
      dir: project({ "compose.yaml": "services:\n  gw:\n    image: x\n    ports: ['${GW_PORT:-8280}:8280']" }),
      env: {},
    })
    expect(c.services.gw!.port).toBe(8280)
  })

  test("global and per-service env files, resolved against orbit.yaml", () => {
    const dir = project({
      "common.env": "A=common\nB=common",
      "api.env": 'B=api\nC="line1\\nline2"',
      "orbit.yaml": `
env_file: common.env
env: { G: inline }
services:
  api:
    cmd: x
    env_file: [api.env, { path: missing.env, required: false }]
    env: { C: override }
  job:
    cmd: y
`,
    })
    const c = loadConfig({ dir, env: {} })
    expect(c.services.api!.envFiles.map((f) => f.path)).toEqual([
      join(dir, "common.env"),
      join(dir, "api.env"),
      join(dir, "missing.env"),
    ])
    expect(c.services.job!.envFiles.map((f) => f.path)).toEqual([join(dir, "common.env")])
    expect(readEnvFiles(c.services.api!.envFiles)).toEqual({ A: "common", B: "api", C: "line1\nline2" })
  })

  test("a missing required env file is a config error", () => {
    const dir = project({ "orbit.yaml": "services:\n  api: { cmd: x, env_file: nope.env }" })
    expect(() => loadConfig({ dir })).toThrow(/env_file not found/)
  })
})
