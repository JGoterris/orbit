import type { ComponentProps, ReactNode } from "react"
import type { LogLine } from "../../core/logs.ts"
import type { Supervisor } from "../../core/supervisor.ts"
import type { LogView } from "../LogView.tsx"

/** Every focusable panel any view can show. Views list the ones they use in `ViewDef.panes`. */
export type Pane = "services" | "detail" | "logs" | "graph"

/** What the shell hands to a view so it can render its body. */
export interface ViewContext {
  sup: Supervisor
  names: string[]
  selected: string
  setSelected: (name: string) => void
  tick: number
  width: number
  height: number
  sidebarW: number
  detailRows: number
  zoomed: boolean
  focus: Pane
  setFocus: (pane: Pane) => void
  logs: {
    /** service whose logs the view shows (undefined = all) */
    service: string | undefined
    scope: "selected" | "all"
    lines: (service: string | undefined) => readonly LogLine[]
    filter: string
    scrollBack: number
    onScroll: (delta: number) => void
    showTime: boolean
    wrap: boolean
    /** copy-mode / search props shared by every LogView */
    extras: Pick<ComponentProps<typeof LogView>, "cursor" | "anchor" | "freeze" | "search" | "current" | "onSelect">
  }
}

export interface ViewDef {
  id: string
  label: string
  /** panels in `tab` order; the shell draws the service sidebar when it contains "services" */
  panes: Pane[]
  defaultPane: Pane
  /** narrow sidebar (the view needs the room) */
  compactSidebar?: boolean
  render(ctx: ViewContext): ReactNode
}
