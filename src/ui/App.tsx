import type { KeyEvent } from "@opentui/core"
import { useKeyboard, useRenderer, useTerminalDimensions } from "@opentui/react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { openUrl } from "../core/exec.ts"
import { findGitRoot } from "../core/git.ts"
import type { Supervisor } from "../core/supervisor.ts"
import { GraphView, neighbourInDirection, useGraphLayout } from "./GraphView.tsx"
import { useSupervisorVersion, useTick } from "./hooks.ts"
import { LogView } from "./LogView.tsx"
import { CommandPalette, ConfirmOverlay, filterCommands, HelpOverlay, type Command } from "./Overlays.tsx"
import { ServiceDetail } from "./ServiceDetail.tsx"
import { ServiceList } from "./ServiceList.tsx"
import { statusStyle, theme } from "./theme.ts"

type View = "dashboard" | "graph" | "logs"
type Mode = "normal" | "palette" | "filter" | "help" | "quit" | "stopping" | "external"

const VIEWS: Array<{ id: View; label: string }> = [
  { id: "dashboard", label: "Dashboard" },
  { id: "graph", label: "Graph" },
  { id: "logs", label: "Logs" },
]

interface Props {
  sup: Supervisor
  onQuit: () => Promise<void> | void
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
  const [logScope, setLogScope] = useState<"selected" | "all">("all")
  const [filter, setFilter] = useState("")
  const [scrollBack, setScrollBack] = useState(0)
  const [showTime, setShowTime] = useState(false)
  const [query, setQuery] = useState("")
  const [paletteIndex, setPaletteIndex] = useState(0)
  const [toast, setToast] = useState<{ text: string; color: string } | undefined>()
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(undefined)

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
    if (sup.ownedRunningCount() === 0) return void onQuit()
    setMode("quit")
  }, [sup, onQuit])

  const doQuit = useCallback(async () => {
    setMode("stopping")
    await onQuit()
  }, [onQuit])

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
            },
          },
        ]
        if (svc.port || svc.url) items.push({ id: `open-${n}`, label: `Open ${n} in browser`, hint: svc.url ?? `:${svc.port}`, run: () => openService(n) })
        if (findGitRoot(svc.cwd)) items.push({ id: `git-${n}`, label: `Open ${n} in lazygit`, hint: "L", run: () => void openLazygit(n) })
        return items
      }),
      { id: "clear-logs", label: "Clear all logs", run: () => sup.clearLogs() },
      { id: "toggle-time", label: "Toggle log timestamps", hint: "t", run: () => setShowTime((v) => !v) },
      { id: "help", label: "Show keyboard shortcuts", hint: "?", run: () => setMode("help") },
      { id: "quit", label: "Quit orbit", hint: "q", run: requestQuit },
    ]
    return list
  }, [sup, names, run, openService, openLazygit, requestQuit])

  const matches = useMemo(() => filterCommands(commands, query), [commands, query])

  const moveSelection = (delta: number) =>
    setSelected((cur) => names[(names.indexOf(cur) + delta + names.length) % names.length]!)

  useKeyboard((key: KeyEvent) => {
    const ch = key.sequence
    if (mode === "stopping" || mode === "external") return
    if (mode === "help") return setMode("normal")
    if (mode === "quit") {
      if (ch === "y" || ch === "Y" || key.name === "return") void doQuit()
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
      const i = VIEWS.findIndex((v) => v.id === view)
      return setView(VIEWS[(i + (key.shift ? VIEWS.length - 1 : 1)) % VIEWS.length]!.id)
    }

    if (view === "graph" && ["up", "down", "left", "right"].includes(key.name)) {
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
    if (ch === "o") return openService(selected)
    if (ch === "L") return void openLazygit(selected)
    if (key.name === "return" || ch === "l") {
      setLogScope("selected")
      setScrollBack(0)
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
    if (key.name === "pageup") return setScrollBack((v) => v + Math.max(5, height - 12))
    if (key.name === "pagedown") return setScrollBack((v) => Math.max(0, v - Math.max(5, height - 12)))
  })

  function openPalette() {
    setQuery("")
    setPaletteIndex(0)
    setMode("palette")
  }

  useEffect(() => setPaletteIndex(0), [query])
  useEffect(() => setScrollBack(0), [selected, logScope, view])

  const sidebarW = view === "graph" ? Math.min(30, Math.max(22, Math.floor(width * 0.18))) : Math.min(52, Math.max(40, Math.floor(width * 0.3)))
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
          {VIEWS.map((v, i) =>
            v.id === view ? (
              <strong key={v.id} fg={theme.bg} bg={theme.accent}>{` ${i + 1} ${v.label} `}</strong>
            ) : (
              <span key={v.id} fg={theme.muted}>{` ${i + 1} ${v.label} `}</span>
            ),
          )}
        </text>
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
        <ServiceList sup={sup} names={names} selected={selected} onSelect={setSelected} tick={tick} width={sidebarW} focused={view !== "logs" || logScope === "selected"} compact={view === "graph"} />
        <box flexGrow={1} flexDirection="column">
          {view === "dashboard" && selected ? (
            <>
              <ServiceDetail sup={sup} name={selected} width={width - sidebarW} />
              <LogView
                lines={sup.logs.lines(selected)}
                service={selected}
                names={names}
                filter={filter}
                scrollBack={scrollBack}
                onScroll={(d) => setScrollBack((v) => Math.max(0, v + d))}
                title={`Logs · ${selected}`}
                focused={false}
                showTime={showTime}
              />
            </>
          ) : null}
          {view === "graph" ? <GraphView sup={sup} selected={selected} onSelect={setSelected} tick={tick} focused /> : null}
          {view === "logs" ? (
            <LogView
              lines={sup.logs.lines(logService)}
              service={logService}
              names={names}
              filter={filter}
              scrollBack={scrollBack}
              onScroll={(d) => setScrollBack((v) => Math.max(0, v + d))}
              title={`${logTitle}  (a: ${logScope === "all" ? "only selected" : "all"})`}
              focused
              showTime={showTime}
            />
          ) : null}
        </box>
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
      {mode === "quit" || mode === "stopping" ? (
        <ConfirmOverlay
          width={width}
          busy={mode === "stopping"}
          message={
            mode === "stopping"
              ? `Stopping ${sup.ownedRunningCount()} service(s)…`
              : `${sup.ownedRunningCount()} service(s) running. Stop them and quit?` +
                (sup.names.some((n) => sup.isAdopted(n)) ? " (attached containers stay up)" : "")
          }
        />
      ) : null}
    </box>
  )
}
