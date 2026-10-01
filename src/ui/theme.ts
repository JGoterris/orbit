import type { ServiceType } from "../config/schema.ts"
import type { Status } from "../core/supervisor.ts"

export const theme = {
  bg: "#11121b",
  panel: "#161824",
  panelAlt: "#1c1f2e",
  selection: "#283052",
  border: "#2f3450",
  borderFocus: "#7aa2f7",
  text: "#c0caf5",
  muted: "#7a82ab",
  dim: "#4b5275",
  accent: "#7aa2f7",
  accent2: "#bb9af7",
  cyan: "#7dcfff",
  green: "#9ece6a",
  yellow: "#e0af68",
  orange: "#ff9e64",
  red: "#f7768e",
  edge: "#3b4261",
  upstream: "#7dcfff",
  downstream: "#bb9af7",
} as const

export const statusStyle: Record<Status, { icon: string; color: string; label: string }> = {
  stopped: { icon: "○", color: theme.dim, label: "stopped" },
  waiting: { icon: "◌", color: theme.yellow, label: "waiting" },
  starting: { icon: "◐", color: theme.yellow, label: "starting" },
  running: { icon: "●", color: theme.cyan, label: "running" },
  healthy: { icon: "●", color: theme.green, label: "healthy" },
  unhealthy: { icon: "●", color: theme.orange, label: "unhealthy" },
  stopping: { icon: "◑", color: theme.yellow, label: "stopping" },
  exited: { icon: "○", color: theme.muted, label: "exited" },
  crashed: { icon: "✖", color: theme.red, label: "crashed" },
  failed: { icon: "✖", color: theme.red, label: "failed" },
}

/** Status style, with finished oneshot tasks shown as done instead of exited. */
export function styleFor(status: Status, oneshot?: boolean) {
  if (oneshot && status === "exited") return { icon: "✓", color: theme.green, label: "done" }
  if (oneshot && status === "starting") return { ...statusStyle.starting, label: "running" }
  return statusStyle[status]
}

const SPINNER = ["◐", "◓", "◑", "◒"]

/** Icon for a status; transitional states spin. */
export function statusIcon(status: Status, tick: number, oneshot?: boolean): string {
  if (status === "starting" || status === "stopping" || status === "waiting") return SPINNER[tick % SPINNER.length]!
  return styleFor(status, oneshot).icon
}

export const typeBadge: Record<ServiceType, { label: string; color: string }> = {
  process: { label: "proc", color: theme.accent },
  docker: { label: "dock", color: theme.cyan },
  compose: { label: "comp", color: theme.accent2 },
}

/** Stable color per service name, for prefixes in the combined log view. */
const SERVICE_COLORS = ["#7aa2f7", "#9ece6a", "#e0af68", "#bb9af7", "#7dcfff", "#ff9e64", "#73daca", "#f7768e", "#c3e88d", "#89ddff"]
export function serviceColor(name: string, names: readonly string[]): string {
  const i = names.indexOf(name)
  return SERVICE_COLORS[(i === -1 ? 0 : i) % SERVICE_COLORS.length]!
}

const BARS = "▁▂▃▄▅▆▇█"
export function sparkline(values: readonly number[], width: number, max?: number): string {
  const vs = values.slice(-width)
  const top = max ?? Math.max(1e-9, ...vs)
  const line = vs.map((v) => BARS[Math.min(BARS.length - 1, Math.max(0, Math.round((v / top) * (BARS.length - 1))))]).join("")
  return line.padStart(width, " ")
}

/** Multi-row bar chart, newest value on the right. Returns `rows` strings of exactly `width` chars, top row first. */
export function areaChart(values: readonly number[], width: number, rows: number, max?: number): string[] {
  const vs = values.slice(-width)
  const top = max ?? Math.max(1e-9, ...vs)
  const levels = vs.map((v) => Math.round(Math.min(1, Math.max(0, v / top)) * rows * 8))
  const out: string[] = []
  for (let r = rows - 1; r >= 0; r--) {
    const line = levels
      .map((l) => {
        const cell = l - r * 8
        return cell >= 8 ? "█" : cell > 0 ? BARS[cell - 1]! : r === 0 ? "▁" : " "
      })
      .join("")
    out.push(line.padStart(width, r === 0 ? "▁" : " "))
  }
  return out
}

export function fit(s: string, width: number): string {
  if (width <= 0) return ""
  return s.length > width ? s.slice(0, Math.max(0, width - 1)) + "…" : s.padEnd(width, " ")
}
