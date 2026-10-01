import type { KeyEvent } from "@opentui/core"
import { useKeyboard, useRenderer, useTerminalDimensions } from "@opentui/react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { resolveEnv } from "../config/envFiles.ts"
import { openUrl } from "../core/exec.ts"
import { findGitRoot } from "../core/git.ts"
import type { Supervisor } from "../core/supervisor.ts"
import { GraphView, neighbourInDirection, useGraphLayout } from "./GraphView.tsx"
import { useSupervisorVersion, useTick } from "./hooks.ts"
import { LogView } from "./LogView.tsx"
import { CommandPalette, ConfirmOverlay, EnvOverlay, envPageSize, filterCommands, HelpOverlay, type Command } from "./Overlays.tsx"
import { ServiceDetail } from "./ServiceDetail.tsx"
import { ServiceList } from "./ServiceList.tsx"
import { statusStyle, theme } from "./theme.ts"

type View = "dashboard" | "graph" | "logs"
type Mode = "normal" | "palette" | "filter" | "help" | "env" | "quit" | "stopping" | "external"

type Pane = "services" | "detail" | "logs" | "graph"

const PANES: Record<View, Pane[]> = {
  dashboard: ["services", "detail", "logs"],
  graph: ["services", "graph"],
  logs: ["services", "logs"],
}
const MIN_SIDEBAR = 16
const MIN_DETAIL = 3
const DEFAULT_DETAIL = 9
const DEFAULT_PANE: Record<View, Pane> = { dashboard: "services", graph: "graph", logs: "logs" }

const VIEWS: Array<{ id: View; label: string }> = [
  { id: "dashboard", label: "Dashboard" },
  { id: "graph", label: "Graph" },
  { id: "logs", label: "Logs" },
]

interface Props {
  sup: Supervisor
  onQuit: (how: "stop" | "detach") => Promise<void> | void
}

export function App({ sup, onQuit }: Props) {
  useSupervisorVersion(sup)
  const tick = useTick(250)
  const { width, height } = useTerminalDimensions()
  const renderer = useRenderer()
  const layout = useGraphLayout(sup)
  const names = sup.order

  const [selected, setSelected] = useState(names[0] ?? "")
  const [view, setView] = useState<View>("dashboard")
  const [mode, setMode] = useState<Mode>("normal")
  const [focus, setFocus] = useState<Pane>("services")
  const [zoomed, setZoomed] = useState(false)
  const [logScope, setLogScope] = useState<"selected" | "all">("all")
  const [filter, setFilter] = useState("")
  const [scrollBack, setScrollBack] = useState(0)
  const [showTime, setShowTime] = useState(false)
  const [query, setQuery] = useState("")
  const [paletteIndex, setPaletteIndex] = useState(0)
  const [envScroll, setEnvScroll] = useState(0)
  const [envReveal, setEnvReveal] = useState(false)
  const [toast, setToast] = useState<{ text: string; color: string } | undefined>()
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(undefined)

  const [sidebarDelta, setSidebarDelta] = useState<Record<View, number>>({ dashboard: 0, graph: 0, logs: 0 })
  const [detailH, setDetailH] = useState<number | undefined>()

  const defaultSidebarW =
    view === "graph" ? Math.min(30, Math.max(22, Math.floor(width * 0.18))) : Math.min(52, Math.max(40, Math.floor(width * 0.3)))
  const clampSidebar = (w: number) => Math.max(MIN_SIDEBAR, Math.min(w, Math.max(MIN_SIDEBAR, width - 30)))
  const clampDetail = (h: number) => Math.max(MIN_DETAIL, Math.min(h, Math.max(MIN_DETAIL, height - 10)))
  const sidebarW = clampSidebar(defaultSidebarW + sidebarDelta[view])
  const detailRows = clampDetail(detailH ?? DEFAULT_DETAIL)

  // which divider the focused panel controls, and whether growing the panel grows or shrinks that divider
  const resizeTarget = (): { divider: "sidebar" | "detail"; sign: 1 | -1 } | undefined => {
    if (focus === "services") return { divider: "sidebar", sign: 1 }
    if (focus === "graph" || (focus === "logs" && view === "logs")) return { divider: "sidebar", sign: -1 }
    if (focus === "detail") return { divider: "detail", sign: 1 }
    if (focus === "logs" && view === "dashboard") return { divider: "detail", sign: -1 }
  }

  const resize = (dir: 1 | -1) => {
    const t = resizeTarget()
    if (!t || zoomed) return
    if (t.divider === "sidebar") setSidebarDelta((d) => ({ ...d, [view]: clampSidebar(sidebarW + 2 * dir * t.sign) - defaultSidebarW }))
    else setDetailH(clampDetail(detailRows + dir * t.sign))
  }

  const resetSize = () => {
    const t = resizeTarget()
    if (!t || zoomed) return
    if (t.divider === "sidebar") setSidebarDelta((d) => ({ ...d, [view]: 0 }))
    else setDetailH(undefined)
  }

  const resizeRef = useRef({ resize, resetSize })
  resizeRef.current = { resize, resetSize }

  const notify = useCallback((text: string, color: string = theme.text) => {
    setToast({ text, color })
    clearTimeout(toastTimer.current)
    toastTimer.current = setTimeout(() => setToast(undefined), 3500)
  }, [])

  // surface failures/crashes as toasts
  useEffect(() => {
    const last = new Map(names.map((n) => [n, sup.state(n).status]))
    const onChange = (name?: string) => {
      if (!name) return
      const st = sup.state(name)
      const prev = last.get(name)
      last.set(name, st.status)
      if (prev === st.status) return
      if (st.status === "crashed" || st.status === "failed") notify(`✖ ${name}: ${st.error ?? st.status}`, theme.red)
      else if (st.status === "unhealthy") notify(`● ${name} is unhealthy${st.health ? `: ${st.health}` : ""}`, theme.orange)
    }
    sup.on("change", onChange)
    return () => {
      sup.off("change", onChange)
    }
  }, [sup, names, notify])

  const run = useCallback(
    (label: string, p: Promise<unknown>) => {
      p.catch((err) => notify(`${label}: ${(err as Error).message}`, theme.red))
    },
    [notify],
  )

  const openService = useCallback(
    (name: string) => {
      const svc = sup.service(name)
      const url = svc.url ?? (svc.port ? `http://localhost:${svc.port}` : undefined)
      if (!url) return notify(`${name} has no port or url`, theme.yellow)
      notify(`opening ${url}`, theme.accent)
      void openUrl(url).then((ok) => ok || notify(`could not open ${url} (no xdg-open/wslview)`, theme.red))
    },
    [sup, notify],
  )

  const openLazygit = useCallback(
    async (name: string) => {
      if (!Bun.which("lazygit")) return notify("lazygit is not installed", theme.yellow)
      const root = findGitRoot(sup.service(name).cwd)
      if (!root) return notify(`${name} is not in a git repository`, theme.yellow)
      setMode("external")
      renderer.suspend()
      try {
        const proc = Bun.spawn(["lazygit", "-p", root], { stdio: ["inherit", "inherit", "inherit"] })
        await proc.exited
      } catch (err) {
        notify(`lazygit: ${(err as Error).message}`, theme.red)
      } finally {
        renderer.resume()
        setMode("normal")
      }
    },
    [sup, renderer, notify],
  )

  const requestQuit = useCallback(() => {
    if (sup.ownedRunningCount() === 0) return void onQuit("stop")
    setMode("quit")
  }, [sup, onQuit])

  const doQuit = useCallback(
    async (how: "stop" | "detach") => {
      if (how === "stop") setMode("stopping")
      await onQuit(how)
    },
    [onQuit],
  )

  const commands = useMemo<Command[]>(() => {
    const list: Command[] = [
      { id: "start-all", label: "Start all services", hint: "S", run: () => run("start all", sup.startAll()) },
      { id: "stop-all", label: "Stop all services", hint: "X", run: () => run("stop all", sup.stopAll()) },
      {
        id: "restart-all",
        label: "Restart all running services",
        hint: "R",
        run: () => run("restart", Promise.all(names.filter((n) => sup.isUp(n)).map((n) => sup.restart(n)))),
      },
      ...VIEWS.map((v, i) => ({ id: `view-${v.id}`, label: `View: ${v.label}`, hint: String(i + 1), run: () => setView(v.id) })),
      { id: "grow-panel", label: "Grow focused panel", hint: "+", run: () => resizeRef.current.resize(1) },
      { id: "shrink-panel", label: "Shrink focused panel", hint: "-", run: () => resizeRef.current.resize(-1) },
      {
        id: "reset-sizes",
        label: "Reset panel sizes",
        hint: "=",
        run: () => {
          setSidebarDelta({ dashboard: 0, graph: 0, logs: 0 })
          setDetailH(undefined)
        },
      },
      { id: "toggle-zoom", label: "Toggle zoom of the focused panel", hint: "z", run: () => setZoomed((v) => !v) },
      ...Object.entries(sup.config.groups).flatMap(([g, members]) => [
        { id: `group-start-${g}`, label: `Start group ${g}`, hint: members.join(","), run: () => run(g, sup.startMany(members)) },
        {
          id: `group-stop-${g}`,
          label: `Stop group ${g}`,
          hint: members.join(","),
          run: () => run(g, Promise.all(members.map((m) => sup.stop(m)))),
        },
      ]),
      ...names.flatMap((n) => {
        const svc = sup.service(n)
        const items: Command[] = [
          { id: `start-${n}`, label: `Start ${n}`, hint: svc.dependsOn.length ? `+ ${svc.dependsOn.join(",")}` : "", run: () => run(n, sup.start(n)) },
          { id: `stop-${n}`, label: `Stop ${n}`, run: () => run(n, sup.stop(n)) },
          { id: `restart-${n}`, label: `Restart ${n}`, run: () => run(n, sup.restart(n)) },
          {
            id: `logs-${n}`,
            label: `Logs of ${n}`,
            run: () => {
              setSelected(n)
              setLogScope("selected")
              setView("logs")
              setFocus("logs")
            },
          },
        ]
        if (svc.port || svc.url) items.push({ id: `open-${n}`, label: `Open ${n} in browser`, hint: svc.url ?? `:${svc.port}`, run: () => openService(n) })
        if (findGitRoot(svc.cwd)) items.push({ id: `git-${n}`, label: `Open ${n} in lazygit`, hint: "L", run: () => void openLazygit(n) })
        return items
      }),
      { id: "clear-logs", label: "Clear all logs", run: () => sup.clearLogs() },
      { id: "toggle-time", label: "Toggle log timestamps", hint: "t", run: () => setShowTime((v) => !v) },
      { id: "env", label: "Show environment variables", hint: "e", run: openEnv },
      { id: "help", label: "Show keyboard shortcuts", hint: "?", run: () => setMode("help") },
      { id: "quit", label: "Quit orbit", hint: "q", run: requestQuit },
      {
        id: "quit-detach",
        label: "Quit orbit and leave services running",
        hint: "q d",
        run: () => void (sup.ownedRunningCount() ? doQuit("detach") : onQuit("stop")),
      },
    ]
    return list
  }, [sup, names, selected, run, openService, openLazygit, requestQuit, doQuit, onQuit])

  const matches = useMemo(() => filterCommands(commands, query), [commands, query])

  const moveSelection = (delta: number) =>
    setSelected((cur) => names[(names.indexOf(cur) + delta + names.length) % names.length]!)

  useKeyboard((key: KeyEvent) => {
    const ch = key.sequence
    if (mode === "stopping" || mode === "external") return
    if (mode === "help") return setMode("normal")
    if (mode === "env") {
      const n = selected ? resolveEnv(sup.service(selected), sup.config.root).entries.length : 0
      const page = envPageSize(height)
      const clamp = (v: number) => Math.max(0, Math.min(v, Math.max(0, n - page)))
      if (key.name === "escape" || ch === "e" || ch === "q") return setMode("normal")
      if (ch === "v") return setEnvReveal((v) => !v)
      if (key.name === "down" || ch === "j") return setEnvScroll((v) => clamp(v + 1))
      if (key.name === "up" || ch === "k") return setEnvScroll((v) => clamp(v - 1))
      if (key.name === "pagedown" || (key.ctrl && key.name === "d")) return setEnvScroll((v) => clamp(v + page))
      if (key.name === "pageup" || (key.ctrl && key.name === "u")) return setEnvScroll((v) => clamp(v - page))
      if (ch === "g" || key.name === "home") return setEnvScroll(0)
      if (ch === "G" || key.name === "end") return setEnvScroll(clamp(n))
      return
    }
    if (mode === "quit") {
      if (ch === "y" || ch === "Y" || ch === "s" || ch === "S" || key.name === "return") void doQuit("stop")
      else if (ch === "d" || ch === "D") void doQuit("detach")
      else if (ch === "n" || key.name === "escape" || ch === "q") setMode("normal")
      return
    }
    if (mode === "palette") {
      if (key.name === "escape") return setMode("normal")
      if (key.name === "up" || (key.ctrl && key.name === "p")) return setPaletteIndex((i) => Math.max(0, i - 1))
      if (key.name === "down" || (key.ctrl && key.name === "n")) return setPaletteIndex((i) => Math.min(matches.length - 1, i + 1))
      if (key.name === "return") {
        const cmd = matches[paletteIndex]
        setMode("normal")
        cmd?.run()
      }
      return
    }
    if (mode === "filter") {
      if (key.name === "escape") {
        setFilter("")
        setMode("normal")
      } else if (key.name === "return") setMode("normal")
      return
    }

    // ---- normal mode
    if (key.ctrl && key.name === "c") return requestQuit()
    if (key.ctrl && key.name === "p") return openPalette()
    if (ch === "q") return requestQuit()
    if (ch === ":") return openPalette()
    if (ch === "?") return setMode("help")
    if (ch === "1") return setView("dashboard")
    if (ch === "2") return setView("graph")
    if (ch === "3") return setView("logs")
    if (key.name === "tab") {
      const panes = PANES[view]
      const i = Math.max(0, panes.indexOf(focus))
      return setFocus(panes[(i + (key.shift ? panes.length - 1 : 1)) % panes.length]!)
    }
    if (ch === "z") return setZoomed((v) => !v)
    if (ch === "+") return resize(1)
    if (ch === "-") return resize(-1)
    if (ch === "=") return resetSize()
    if (key.name === "escape" && zoomed) return setZoomed(false)

    const page = Math.max(5, height - (zoomed ? 4 : view === "dashboard" ? detailRows + 3 : 5))
    if (focus === "logs") {
      if (key.name === "down" || ch === "j") return setScrollBack((v) => Math.max(0, v - 1))
      if (key.name === "up" || ch === "k") return setScrollBack((v) => v + 1)
      if (key.ctrl && key.name === "d") return setScrollBack((v) => Math.max(0, v - Math.floor(page / 2)))
      if (key.ctrl && key.name === "u") return setScrollBack((v) => v + Math.floor(page / 2))
      if (ch === "g" || key.name === "home") return setScrollBack(Number.MAX_SAFE_INTEGER)
      if (ch === "G" || key.name === "end") return setScrollBack(0)
    }

    if (view === "graph" && focus === "graph" && ["up", "down", "left", "right"].includes(key.name)) {
      const dir = key.name as "up" | "down" | "left" | "right"
      return setSelected((cur) => neighbourInDirection(layout, cur, dir) ?? cur)
    }
    if (key.name === "down" || ch === "j") return moveSelection(1)
    if (key.name === "up" || ch === "k") return moveSelection(-1)
    if (ch === "g" || key.name === "home") return setSelected(names[0]!)
    if (ch === "G" || key.name === "end") return setSelected(names[names.length - 1]!)

    if (!selected) return
    if (ch === " ") return run(selected, sup.toggle(selected))
    if (ch === "s") return run(selected, sup.start(selected))
    if (ch === "x") return run(selected, sup.stop(selected))
    if (ch === "r") return run(selected, sup.restart(selected))
    if (ch === "S") return run("start all", sup.startAll())
    if (ch === "X") return run("stop all", sup.stopAll())
    if (ch === "R") return commands.find((c) => c.id === "restart-all")!.run()
    if (ch === "e") return openEnv()
    if (ch === "o") return openService(selected)
    if (ch === "L") return void openLazygit(selected)
    if (key.name === "return" || ch === "l") {
      setLogScope("selected")
      setScrollBack(0)
      setFocus("logs")
      return setView("logs")
    }
    if (ch === "a") return setLogScope((s) => (s === "all" ? "selected" : "all"))
    if (ch === "/") return setMode("filter")
    if (ch === "f") return setScrollBack(0)
    if (ch === "t") return setShowTime((v) => !v)
    if (ch === "c") {
      sup.clearLogs(view === "logs" && logScope === "all" ? undefined : selected)
      return notify("logs cleared", theme.muted)
    }
    if (key.name === "pageup") return setScrollBack((v) => v + page)
    if (key.name === "pagedown") return setScrollBack((v) => Math.max(0, v - page))
  })

  function openEnv() {
    if (!selected) return
    setEnvScroll(0)
    setEnvReveal(false)
    setMode("env")
  }

  function openPalette() {
    setQuery("")
    setPaletteIndex(0)
    setMode("palette")
  }

  useEffect(() => setPaletteIndex(0), [query])
  useEffect(() => setScrollBack(0), [selected, logScope, view])
  useEffect(() => {
    setFocus(DEFAULT_PANE[view])
    setZoomed(false)
  }, [view])

  const counts = names.reduce(
    (acc, n) => {
      const s = sup.state(n).status
      if (s === "healthy" || s === "running") acc.up++
      else if (s === "crashed" || s === "failed" || s === "unhealthy") acc.bad++
      else if (s === "starting" || s === "waiting" || s === "stopping") acc.busy++
      return acc
    },
    { up: 0, bad: 0, busy: 0 },
  )

  const logService = view === "logs" && logScope === "all" ? undefined : selected
  const logTitle = logService ? `Logs · ${logService}` : "Logs · all services"

  return (
    <box flexDirection="column" width="100%" height="100%" backgroundColor={theme.bg}>
      {/* header */}
      <box height={1} flexDirection="row" paddingLeft={1} paddingRight={1} backgroundColor={theme.panelAlt}>
        <text>
          <strong fg={theme.accent}>◉ orbit</strong>
          <span fg={theme.dim}> · </span>
          <span fg={theme.text}>{sup.config.name}</span>
          <span fg={theme.dim}>{"    "}</span>
        </text>
        {VIEWS.map((v, i) => (
          <box key={v.id} height={1} onMouseDown={() => setView(v.id)}>
            {v.id === view ? (
              <text>
                <strong fg={theme.bg} bg={theme.accent}>{` ${i + 1} ${v.label} `}</strong>
              </text>
            ) : (
              <text fg={theme.muted}>{` ${i + 1} ${v.label} `}</text>
            )}
          </box>
        ))}
        {zoomed ? <text fg={theme.accent}>{"  ⛶ zoom"}</text> : null}
        <box flexGrow={1} />
        <text>
          <span fg={theme.green}>● {counts.up} up</span>
          {counts.busy ? <span fg={theme.yellow}>{`  ◐ ${counts.busy}`}</span> : null}
          {counts.bad ? <span fg={theme.red}>{`  ✖ ${counts.bad}`}</span> : null}
          <span fg={theme.dim}>{`  ○ ${names.length - counts.up - counts.bad - counts.busy}`}</span>
        </text>
      </box>

      {/* body */}
      <box flexGrow={1} flexDirection="row">
        {!zoomed || focus === "services" ? (
          <ServiceList
            sup={sup}
            names={names}
            selected={selected}
            onSelect={setSelected}
            tick={tick}
            width={zoomed ? width : sidebarW}
            focused={focus === "services"}
            compact={(view === "graph" || sidebarW < 36) && !zoomed}
            onFocus={() => setFocus("services")}
          />
        ) : null}
        {!zoomed || focus !== "services" ? (
          <box flexGrow={1} flexDirection="column">
            {view === "dashboard" && selected ? (
              <>
                {!zoomed || focus === "detail" ? (
                  <ServiceDetail
                    sup={sup}
                    name={selected}
                    width={zoomed ? width : width - sidebarW}
                    focused={focus === "detail"}
                    expanded={zoomed}
                    height={detailRows}
                    rows={zoomed ? height - 6 : detailRows - 2}
                    onFocus={() => setFocus("detail")}
                  />
                ) : null}
                {!zoomed || focus === "logs" ? (
                  <LogView
                    lines={sup.logs.lines(selected)}
                    service={selected}
                    names={names}
                    filter={filter}
                    scrollBack={scrollBack}
                    onScroll={(d) => setScrollBack((v) => Math.max(0, v + d))}
                    title={`Logs · ${selected}`}
                    focused={focus === "logs"}
                    showTime={showTime}
                    onFocus={() => setFocus("logs")}
                  />
                ) : null}
              </>
            ) : null}
            {view === "graph" ? (
              <GraphView sup={sup} selected={selected} onSelect={setSelected} tick={tick} focused={focus === "graph"} onFocus={() => setFocus("graph")} />
            ) : null}
            {view === "logs" ? (
              <LogView
                lines={sup.logs.lines(logService)}
                service={logService}
                names={names}
                filter={filter}
                scrollBack={scrollBack}
                onScroll={(d) => setScrollBack((v) => Math.max(0, v + d))}
                title={`${logTitle}  (a: ${logScope === "all" ? "only selected" : "all"})`}
                focused={focus === "logs"}
                showTime={showTime}
                onFocus={() => setFocus("logs")}
              />
            ) : null}
          </box>
        ) : null}
      </box>

      {/* filter bar */}
      {mode === "filter" ? (
        <box height={1} flexDirection="row" paddingLeft={1} backgroundColor={theme.panelAlt}>
          <text fg={theme.accent}>{"/ "}</text>
          <input
            flexGrow={1}
            focused
            value={filter}
            placeholder="filter logs (regex) · enter to keep · esc to clear"
            onInput={setFilter}
            backgroundColor={theme.panelAlt}
            focusedBackgroundColor={theme.panelAlt}
            textColor={theme.text}
            placeholderColor={theme.dim}
          />
        </box>
      ) : null}

      {/* footer */}
      <box height={1} flexDirection="row" paddingLeft={1} paddingRight={1} backgroundColor={theme.panelAlt}>
        {toast ? (
          <text fg={toast.color}>{toast.text}</text>
        ) : (
          <text>
            {[
              ["space", "start/stop"],
              ["r", "restart"],
              ["S/X", "all"],
              ["l", "logs"],
              ["tab", "focus"],
              ["z", "zoom"],
              ["/", "filter"],
              ["o", "open"],
              [":", "commands"],
              ["?", "help"],
              ["q", "quit"],
            ].flatMap(([k, v]) => [
              <span key={`k${k}`} fg={theme.accent}>
                {k}
              </span>,
              <span key={`v${k}`} fg={theme.dim}>{` ${v}   `}</span>,
            ])}
          </text>
        )}
        <box flexGrow={1} />
        {selected ? (
          <text>
            <span fg={statusStyle[sup.state(selected).status].color}>{statusStyle[sup.state(selected).status].icon} </span>
            <span fg={theme.muted}>{selected}</span>
          </text>
        ) : null}
      </box>

      {mode === "palette" ? <CommandPalette commands={matches} selected={paletteIndex} onQuery={setQuery} width={width} /> : null}
      {mode === "help" ? <HelpOverlay width={width} /> : null}
      {mode === "env" && selected ? (
        <EnvOverlay service={selected} {...resolveEnv(sup.service(selected), sup.config.root)} scroll={envScroll} reveal={envReveal} width={width} height={height} />
      ) : null}
      {mode === "quit" || mode === "stopping" ? (
        <ConfirmOverlay
          width={width}
          busy={mode === "stopping"}
          message={
            mode === "stopping"
              ? `Stopping ${sup.ownedRunningCount()} service(s)…`
              : `${sup.ownedRunningCount()} service(s) running.` +
                (sup.names.some((n) => sup.isAdopted(n)) ? " (attached containers stay up)" : "")
          }
        />
      ) : null}
    </box>
  )
}
