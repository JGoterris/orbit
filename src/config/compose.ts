import { dirname, basename } from "node:path"
import YAML from "yaml"
import { existsSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { asMemSize, asRecord, asStringList, hostPortOf, asEnv, type ServiceConfig } from "./schema.ts"
import { interpolate, parseDotEnv } from "./interpolate.ts"

export const COMPOSE_FILENAMES = ["compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"]

export function defaultComposeProject(file: string): string {
  return basename(dirname(file))
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, "")
}

export function parseComposeFile(
  file: string,
  text: string,
  project?: string,
  env: Record<string, string | undefined> = process.env,
): ServiceConfig[] {
  const dotEnvPath = join(dirname(file), ".env")
  const vars = { ...(existsSync(dotEnvPath) ? parseDotEnv(readFileSync(dotEnvPath, "utf8")) : {}), ...env }
  const doc = interpolate(asRecord(YAML.parse(text), file), vars)
  const services = asRecord(doc.services, `${file}: services`)
  const composeProject = project ?? (typeof doc.name === "string" ? doc.name : defaultComposeProject(file))

  return Object.entries(services).map(([name, raw]) => {
    const path = `${file}: services.${name}`
    const svc = asRecord(raw, path)
    // depends_on may be a list or a mapping { db: { condition: service_healthy } }
    const dependsOn = Array.isArray(svc.depends_on)
      ? asStringList(svc.depends_on, `${path}.depends_on`)
      : Object.keys(asRecord(svc.depends_on, `${path}.depends_on`))
    const ports = asStringList(
      Array.isArray(svc.ports)
        ? svc.ports.map((p) => (typeof p === "object" && p ? `${p.published ?? p.target}:${p.target}` : p))
        : svc.ports,
      `${path}.ports`,
    )
    const port = ports.map(hostPortOf).find((p) => p !== undefined)
    const limits = asRecord(asRecord(asRecord(svc.deploy, `${path}.deploy`).resources, `${path}.deploy.resources`).limits, `${path}.deploy.resources.limits`)
    const memLimit = asMemSize(svc.mem_limit ?? limits.memory, `${path}.mem_limit`)
    const hasHealthcheck = svc.healthcheck !== undefined && asRecord(svc.healthcheck, path).disable !== true

    return {
      name,
      type: "compose",
      description: typeof svc.image === "string" ? svc.image : svc.build ? "build" : undefined,
      cwd: dirname(file),
      env: asEnv(svc.environment, `${path}.environment`),
      envFiles: [],
      dependsOn,
      port,
      health: hasHealthcheck
        ? { container: true, interval: 2000, timeout: 5000 }
        : port
          ? { tcp: String(port), interval: 1000, timeout: 1000 }
          : undefined,
      restart: "no",
      startTimeout: 120_000,
      stopTimeout: 10_000,
      autostart: true,
      memLimit,
      leakDetection: true,
      image: typeof svc.image === "string" ? svc.image : undefined,
      ports,
      volumes: [],
      dockerArgs: [],
      composeFile: file,
      composeProject,
      composeService: name,
    } satisfies ServiceConfig
  })
}
