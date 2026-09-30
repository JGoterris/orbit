import type { HealthCheck, ServiceConfig } from "../config/schema.ts"
import { exec, isPortOpen } from "./exec.ts"

export interface HealthResult {
  ok: boolean
  detail: string
}

function parseTcp(target: string): { host: string; port: number } {
  const idx = target.lastIndexOf(":")
  if (idx === -1) return { host: "127.0.0.1", port: Number(target) }
  return { host: target.slice(0, idx) || "127.0.0.1", port: Number(target.slice(idx + 1)) }
}

export function describeHealth(h: HealthCheck | undefined): string {
  if (!h) return "none"
  if (h.http) return `http ${h.http}`
  if (h.tcp) return `tcp ${h.tcp}`
  if (h.cmd) return `cmd ${h.cmd}`
  if (h.container) return "docker healthcheck"
  return "none"
}

export async function checkHealth(
  h: HealthCheck,
  svc: ServiceConfig,
  containerId: string | undefined,
): Promise<HealthResult> {
  if (h.http) {
    const ctrl = new AbortController()
    const t = setTimeout(() => ctrl.abort(), h.timeout)
    try {
      const res = await fetch(h.http, { signal: ctrl.signal, redirect: "manual" })
      await res.body?.cancel()
      return { ok: res.status < 500, detail: `HTTP ${res.status}` }
    } catch (err) {
      return { ok: false, detail: ctrl.signal.aborted ? "timeout" : ((err as Error).message ?? "error") }
    } finally {
      clearTimeout(t)
    }
  }
  if (h.tcp) {
    const { host, port } = parseTcp(h.tcp)
    const ok = await isPortOpen(port, host, h.timeout)
    return { ok, detail: ok ? `port ${port} open` : `port ${port} closed` }
  }
  if (h.cmd) {
    const res = await exec(["/bin/sh", "-c", h.cmd], {
      cwd: svc.cwd,
      env: { ...process.env, ...svc.env },
      timeout: h.timeout,
    })
    return { ok: res.code === 0, detail: `exit ${res.code}` }
  }
  if (h.container) {
    if (!containerId) return { ok: false, detail: "no container" }
    const res = await exec(
      ["docker", "inspect", "-f", "{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}", containerId],
      { timeout: h.timeout },
    )
    const status = res.stdout.trim()
    return { ok: status === "healthy" || status === "running", detail: status || "unknown" }
  }
  return { ok: true, detail: "" }
}
