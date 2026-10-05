import type { BoxRenderable, MouseEvent } from "@opentui/core"
import { useMemo, useRef } from "react"
import { transitiveDependents, transitiveDeps } from "../core/graph.ts"
import { formatBytes } from "../core/metrics.ts"
import type { SupervisorLike } from "../core/supervisor.ts"
import { useSize } from "./hooks.ts"
import { layoutGraph, paintGraph, toRuns, type GraphLayout, type NodeBox } from "./graphLayout.ts"
import { statusIcon, styleFor, theme } from "./theme.ts"

interface Props {
  sup: SupervisorLike
  selected: string
  onSelect: (name: string) => void
  tick: number
  focused: boolean
  onFocus?: () => void
}

export function useGraphLayout(sup: SupervisorLike): GraphLayout {
  return useMemo(() => layoutGraph(sup.deps, sup.names), [sup])
}

/** Nearest box in a direction, for arrow-key navigation inside the graph. */
export function neighbourInDirection(
  layout: GraphLayout,
  from: string,
  dir: "up" | "down" | "left" | "right",
): string | undefined {
  const cur = layout.boxes.find((b) => b.name === from)
  if (!cur) return
  const cx = (b: NodeBox) => b.x + b.w / 2
  const cy = (b: NodeBox) => b.y + b.h / 2
  let best: { name: string; score: number } | undefined
  for (const b of layout.boxes) {
    if (b.name === from) continue
    const dx = cx(b) - cx(cur)
    const dy = cy(b) - cy(cur)
    const primary = dir === "left" ? -dx : dir === "right" ? dx : dir === "up" ? -dy : dy
    const secondary = dir === "left" || dir === "right" ? Math.abs(dy) : Math.abs(dx)
    if (primary <= 0) continue
    const score = primary + secondary * 2.5
    if (!best || score < best.score) best = { name: b.name, score }
  }
  return best?.name
}

export function GraphView({ sup, selected, onSelect, tick, focused, onFocus }: Props) {
  const layout = useGraphLayout(sup)
  const { ref, size, onSizeChange } = useSize<BoxRenderable>()
  const offset = useRef({ x: 0, y: 0 })

  const upstream = transitiveDeps(sup.deps, selected)
  const downstream = transitiveDependents(sup.deps, selected)

  const grid = paintGraph(layout, {
    text: theme.text,
    defaultEdge: theme.edge,
    edgeColor: (edge) => {
      const [dep, dependent] = edge.split(">") as [string, string]
      if (upstream.has(dep) && (dependent === selected || upstream.has(dependent))) return theme.upstream
      if (downstream.has(dependent) && (dep === selected || downstream.has(dep))) return theme.downstream
      return undefined
    },
    node: (name) => {
      const st = sup.state(name)
      const svc = sup.service(name)
      const style = styleFor(st.status, svc.oneshot)
      const mem = st.mem.length && sup.isUp(name) ? ` ${formatBytes(st.mem[st.mem.length - 1]!)}` : ""
      return {
        icon: statusIcon(st.status, tick, svc.oneshot),
        iconColor: style.color,
        title: name,
        subtitle: `${style.label}${svc.port ? ` :${svc.port}` : ""}${mem}`,
        subtitleColor: style.color,
        border:
          name === selected
            ? theme.accent
            : upstream.has(name)
              ? theme.upstream
              : downstream.has(name)
                ? theme.downstream
                : theme.border,
        selected: name === selected,
      }
    },
  })

  // viewport: centre when it fits, otherwise keep the selected box visible
  const viewW = Math.max(1, size.width - 2)
  const viewH = Math.max(1, size.height - 3)
  const box = layout.boxes.find((b) => b.name === selected)
  const o = offset.current
  const measured = size.width > 0 && size.height > 0
  if (layout.width <= viewW) o.x = 0
  else if (box && measured) {
    if (box.x - 2 < o.x) o.x = Math.max(0, box.x - 2)
    if (box.x + box.w + 2 > o.x + viewW) o.x = Math.min(layout.width - viewW, box.x + box.w + 2 - viewW)
  }
  if (layout.height <= viewH) o.y = 0
  else if (box && measured) {
    if (box.y - 1 < o.y) o.y = Math.max(0, box.y - 1)
    if (box.y + box.h + 1 > o.y + viewH) o.y = Math.min(layout.height - viewH, box.y + box.h + 1 - viewH)
  }
  // never scroll past the edges (e.g. after the first render, when the size was still unknown)
  o.x = Math.max(0, Math.min(o.x, layout.width - viewW))
  o.y = Math.max(0, Math.min(o.y, layout.height - viewH))
  const padX = Math.max(0, Math.floor((viewW - layout.width) / 2))
  const padY = Math.max(0, Math.floor((viewH - layout.height) / 2))
  const rows = grid.slice(o.y, o.y + viewH)

  const onMouseDown = (e: MouseEvent) => {
    onFocus?.()
    const r = ref.current
    if (!r) return
    const gx = e.x - r.x - 1 - padX + o.x
    const gy = e.y - r.y - 1 - padY + o.y
    const hit = layout.boxes.find((b) => gx >= b.x && gx < b.x + b.w && gy >= b.y && gy < b.y + b.h)
    if (hit) onSelect(hit.name)
  }

  const up = sup.names.filter((n) => sup.isUp(n)).length
  return (
    <box
      ref={ref}
      onSizeChange={onSizeChange}
      flexGrow={1}
      flexDirection="column"
      border
      borderStyle="rounded"
      borderColor={focused ? theme.borderFocus : theme.border}
      backgroundColor={theme.panel}
      title={` Dependency graph · ${up}/${sup.names.length} up `}
      titleColor={theme.text}
      onMouseDown={onMouseDown}
    >
      <box flexGrow={1} flexDirection="column" paddingTop={padY} paddingLeft={padX}>
        {rows.map((row, y) => (
          <text key={y}>
            {toRuns(row, o.x, o.x + viewW - padX).map((run, i) =>
              run.bold ? (
                <strong key={i} fg={run.fg}>
                  {run.text}
                </strong>
              ) : (
                <span key={i} fg={run.fg}>
                  {run.text}
                </span>
              ),
            )}
          </text>
        ))}
      </box>
      <text>
        <span fg={theme.dim}> ──▶ starts before   </span>
        <span fg={theme.upstream}>■ needs ({upstream.size})   </span>
        <span fg={theme.downstream}>■ used by ({downstream.size})   </span>
        <span fg={theme.dim}>←↑↓→ move · space start/stop · enter logs</span>
      </text>
    </box>
  )
}
