import { describe, expect, test } from "bun:test"
import type { OrbitConfig, ServiceConfig } from "../src/config/schema.ts"
import { exec } from "../src/core/exec.ts"
import { Supervisor } from "../src/core/supervisor.ts"

const hasDocker = (await exec(["docker", "image", "inspect", "redis:7-alpine"], { timeout: 5000 })).code === 0

function redis(): OrbitConfig {
  const svc: ServiceConfig = {
    name: "cache", type: "docker", image: "redis:7-alpine", cwd: process.cwd(), env: {}, envFiles: [],
    dependsOn: [], restart: "no", startTimeout: 20_000, stopTimeout: 2000, autostart: true,
    ports: [], volumes: [], dockerArgs: [],
  }
  return { name: "adopt-test", root: process.cwd(), services: { cache: svc }, groups: {} }
}

const running = async (name: string) =>
  (await exec(["docker", "inspect", "-f", "{{.State.Running}}", name], { timeout: 5000 })).stdout.trim() === "true"

describe.skipIf(!hasDocker)("docker", () => {
  test("containers that were already running are attached, and left running on quit", async () => {
    const name = "orbit-adopt-test-cache"
    await exec(["docker", "rm", "-f", name])
    await exec(["docker", "run", "-d", "--name", name, "redis:7-alpine"])
    try {
      const sup = new Supervisor(redis())
      await sup.init()
      expect(sup.state("cache").status).toBe("running")
      expect(sup.isAdopted("cache")).toBe(true)
      expect(sup.ownedRunningCount()).toBe(0)
      await sup.dispose()
      expect(await running(name)).toBe(true)

      // an explicit stop does stop it
      const again = new Supervisor(redis())
      await again.init()
      await again.stop("cache")
      expect(await running(name)).toBe(false)
      await again.dispose()
    } finally {
      await exec(["docker", "rm", "-f", name])
    }
  }, 60_000)

  test("containers orbit started are stopped and removed on quit", async () => {
    const sup = new Supervisor(redis())
    await sup.init()
    expect(await sup.start("cache")).toBe(true)
    expect(sup.ownedRunningCount()).toBe(1)
    await sup.dispose()
    expect(await running("orbit-adopt-test-cache")).toBe(false)
  }, 60_000)
})
