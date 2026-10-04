import type { ServiceType } from "../config/schema.ts"
import type { Status } from "../core/supervisor.ts"
import { DEFAULT_THEME, THEMES, type Palette } from "./themes.ts"

/** The active palette. Mutable on purpose: `applyTheme` swaps its values, components read it at render time. */
export const theme: Palette = { ...THEMES[DEFAULT_THEME]! }

export function applyTheme(p: Palette) {
  Object.assign(theme, p)
}

/** A style whose color is read from the active theme every time it is used. */
const style = (icon: string, label: string, color: () => string) => ({
  icon,
  label,
  get color() {
    return color()
  },
})

export const statusStyle: Record<Status, { icon: string; color: string; label: string }> = {
  stopped: style("○", "stopped", () => theme.dim),
  waiting: style("◌", "waiting", () => theme.yellow),
  starting: style("◐", "starting", () => theme.yellow),
  running: style("●", "running", () => theme.cyan),
  healthy: style("●", "healthy", () => theme.green),
  unhealthy: style("●", "unhealthy", () => theme.orange),
  stopping: style("◑", "stopping", () => theme.yellow),
  exited: style("○", "exited", () => theme.muted),
  crashed: style("✖", "crashed", () => theme.red),
  failed: style("✖", "failed", () => theme.red),
}

/** Status style, with finished oneshot tasks shown as done instead of exited. */
export function styleFor(status: Status, oneshot?: boolean) {
  if (oneshot && status === "exited") return { icon: "✓", color: theme.green, label: "done" }
  if (oneshot && status === "starting") return { icon: statusStyle.starting.icon, color: statusStyle.starting.color, label: "running" }
  return statusStyle[status]
}

const SPINNER = ["◐", "◓", "◑", "◒"]

/** Icon for a status; transitional states spin. */
export function statusIcon(status: Status, tick: number, oneshot?: boolean): string {
  if (status === "starting" || status === "stopping" || status === "waiting") return SPINNER[tick % SPINNER.length]!
  return styleFor(status, oneshot).icon
}

export const typeBadge: Record<ServiceType, { label: string; color: string }> = {
  process: { label: "proc", get color() { return theme.accent } },
  docker: { label: "dock", get color() { return theme.cyan } },
  compose: { label: "comp", get color() { return theme.accent2 } },
}

/** Stable color per service name, for prefixes in the combined log view. */
export function serviceColor(name: string, names: readonly string[]): string {
  const i = names.indexOf(name)
  return theme.services[(i === -1 ? 0 : i) % theme.services.length]!
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

/** Blends `color` over `base` (`#rrggbb` both), `amount` 0..1 of `color`: a tint for diff backgrounds. */
export function mix(base: string, color: string, amount: number): string {
  const c = (hex: string, i: number) => Number.parseInt(hex.slice(1 + i * 2, 3 + i * 2), 16)
  return (
    "#" +
    [0, 1, 2]
      .map((i) => Math.round(c(base, i) * (1 - amount) + c(color, i) * amount).toString(16).padStart(2, "0"))
      .join("")
  )
}
