import { GraphView } from "../GraphView.tsx"
import { LogView } from "../LogView.tsx"
import { ServiceDetail } from "../ServiceDetail.tsx"
import { GitView, gitHints } from "./Git.tsx"
import { theme } from "../theme.ts"
import type { ViewContext, ViewDef } from "./types.ts"

export type { Pane, ViewContext, ViewDef } from "./types.ts"

function Dashboard(c: ViewContext) {
  if (!c.selected) {
    return (
      <box flexGrow={1} border borderStyle="rounded" borderColor={theme.border} backgroundColor={theme.panel} padding={1}>
        <text fg={theme.muted}>This project has no services. Add an orbit.yaml or a docker-compose.yml.</text>
      </box>
    )
  }
  const { logs } = c
  return (
    <>
      {!c.zoomed || c.focus === "detail" ? (
        <ServiceDetail
          sup={c.sup}
          name={c.selected}
          width={c.zoomed ? c.width : c.width - c.sidebarW}
          focused={c.focus === "detail"}
          expanded={c.zoomed}
          height={c.detailRows}
          rows={c.zoomed ? c.height - 6 : c.detailRows - 2}
          range={c.range}
          onFocus={() => c.setFocus("detail")}
        />
      ) : null}
      {!c.zoomed || c.focus === "logs" ? (
        <LogView
          lines={logs.lines(c.selected)}
          service={c.selected}
          names={c.names}
          filter={logs.filter}
          scrollBack={logs.scrollBack}
          onScroll={logs.onScroll}
          title={`Logs · ${c.selected}`}
          focused={c.focus === "logs"}
          showTime={logs.showTime}
          wrap={logs.wrap}
          {...logs.extras}
          onFocus={() => c.setFocus("logs")}
        />
      ) : null}
    </>
  )
}

function Graph(c: ViewContext) {
  return <GraphView sup={c.sup} selected={c.selected} onSelect={c.setSelected} tick={c.tick} focused={c.focus === "graph"} onFocus={() => c.setFocus("graph")} />
}

function Logs(c: ViewContext) {
  const { logs } = c
  const title = logs.service ? `Logs · ${logs.service}` : "Logs · all services"
  return (
    <LogView
      lines={logs.lines(logs.service)}
      service={logs.service}
      names={c.names}
      filter={logs.filter}
      scrollBack={logs.scrollBack}
      onScroll={logs.onScroll}
      title={`${title}  (a: ${logs.scope === "all" ? "only selected" : "all"})`}
      focused={c.focus === "logs"}
      showTime={logs.showTime}
      wrap={logs.wrap}
      {...logs.extras}
      onFocus={() => c.setFocus("logs")}
    />
  )
}

/** The views, in header / number-key order: the n-th one answers to the key `n`. */
export const VIEWS: ViewDef[] = [
  { id: "dashboard", label: "Dashboard", panes: ["services", "detail", "logs"], defaultPane: "services", render: Dashboard },
  { id: "graph", label: "Graph", panes: ["services", "graph"], defaultPane: "graph", compactSidebar: true, render: Graph },
  { id: "logs", label: "Logs", panes: ["services", "logs"], defaultPane: "logs", render: Logs },
  {
    id: "git",
    label: "Git",
    panes: ["repos", "changes", "branches", "commits", "stash", "gitdiff", "gitlog"],
    defaultPane: "repos",
    render: (c) => <GitView ctx={c} />,
    hints: (c) => gitHints(c.focus),
  },
]

export const viewById = (id: string): ViewDef => VIEWS.find((v) => v.id === id) ?? VIEWS[0]!
