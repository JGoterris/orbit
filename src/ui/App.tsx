import type { KeyEvent } from "@opentui/core"
import { useKeyboard, useRenderer, useTerminalDimensions } from "@opentui/react"
import { mkdirSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { resolveEnv } from "../config/envFiles.ts"
import { openUrl } from "../core/exec.ts"
import { findGitRoot } from "../core/git.ts"
import { filterLines, formatLines, matcher } from "../core/logs.ts"
import type { Supervisor } from "../core/supervisor.ts"
import { GraphView, neighbourInDirection, useGraphLayout } from "./GraphView.tsx"
import { useSupervisorVersion, useTick } from "./hooks.ts"
import { clipboard } from "./clipboard.ts"
import { CommandPalette, ConfirmOverlay, EnvOverlay, envPageSize, filterCommands, HelpOverlay, ProjectPicker, projectRows, ThemePicker, type Command } from "./Overlays.tsx"
import { ServiceList } from "./ServiceList.tsx"
import type { GitRepo } from "../core/git/repo.ts"
import { discoverRepos, repoEntries, repoOfService, summarize } from "../core/git/repos.ts"
import { viewById, VIEWS, type Pane, type ViewContext } from "./views/index.tsx"
import type { KeyHandler } from "./views/types.ts"
import { applyTheme, statusStyle, theme } from "./theme.ts"
import { DEFAULT_THEME, THEMES, type Palette } from "./themes.ts"
import { writeUserConfig } from "../core/userConfig.ts"
import { completePath, forgetProject, looksLikePath, projectStatus, readProjects, setPinned, type ProjectEntry, type ProjectStatus } from "../core/projects.ts"

type Mode = "normal" | "palette" | "filter" | "help" | "env" | "quit" | "stopping" | "external" | "copy" | "theme" | "projects" | "switch"

const MIN_SIDEBAR = 16
const MIN_DETAIL = 3
const DEFAULT_DETAIL = 9
const noSidebarDelta = (): Record<string, number> => Object.fromEntries(VIEWS.map((v) => [v.id, 0]))

interface Props {
  sup: Supervisor
  onQuit: (how: "stop" | "detach") => Promise<void> | void
  /** switches to another project; resolves to an error message if it could not (nothing changed then) */
  onOpenProject?: (dir: string, how: "stop" | "detach") => Promise<string | undefined>
  /** open the project picker right away (orbit was started in a folder that is not a project) */
  startWithPicker?: boolean
  /** the git repositories to show; by default those the services live in, `null` turns git off */
  git?: GitRepo | GitRepo[] | null
  /** every selectable theme (built-in first, then the user's); defaults to the built-in ones */
  themes?: Record<string, Palette>
  /** names in `themes` that come from the user's themes dir */
  customThemes?: string[]
  /** theme applied before the first render */
  initialTheme?: string
  /** problems found while loading user themes, shown once as a toast */
  themeErrors?: string[]
}

export function App({ sup, onQuit, onOpenProject, startWithPicker = false, git, themes = THEMES, customThemes = [], initialTheme = DEFAULT_THEME, themeErrors = [] }: Props) {
  useSupervisorVersion(sup)
  const tick = useTick(250)
  const { width, height } = useTerminalDimensions()
  const renderer = useRenderer()
  const layout = useGraphLayout(sup)
  const names = sup.order
  const repos = useMemo(() => (git === null ? [] : git ? repoEntries([git].flat()) : discoverRepos(sup.config)), [git, sup])
  const [repoIndex, setRepoIndex] = useState(0)
  // what the open view wants from the keyboard (see ViewContext.keys / .capture)
  const viewKeys = useRef<KeyHandler | undefined>(undefined)
  const viewCapture = useRef(false)
  const [viewHints, setViewHints] = useState<string[][] | undefined>()

  const [selected, setSelected] = useState(names[0] ?? "")
  const [view, setView] = useState(VIEWS[0]!.id)
  const viewDef = viewById(view)
  const [mode, setMode] = useState<Mode>("normal")
  // `savedTheme` is what config.json holds; `themeName` is what is on screen (differs while previewing)
  const [themeName, setThemeName] = useState(initialTheme)
  const savedTheme = useRef(initialTheme)
  const [themeIndex, setThemeIndex] = useState(0)
  const [focus, setFocus] = useState<Pane>("services")
  const [zoomed, setZoomed] = useState(false)
  const [logScope, setLogScope] = useState<"selected" | "all">("all")
  const [filter, setFilter] = useState("")
  const [scrollBack, setScrollBack] = useState(0)
  const [showTime, setShowTime] = useState(false)
  const [wrap, setWrap] = useState(true)
  // the bar opened by "/" either hides non-matching lines (filter) or highlights matches and lets n/N jump (search)
  const [barKind, setBarKind] = useState<"filter" | "search">("filter")
  const [search, setSearch] = useState("")
  const [match, setMatch] = useState<number | undefined>()
  // copy mode: line cursor and optional selection anchor, both by line seq
  const [copy, setCopy] = useState<{ cursor: number; anchor?: number } | undefined>()
  const [query, setQuery] = useState("")
  const [paletteIndex, setPaletteIndex] = useState(0)
  const [envScroll, setEnvScroll] = useState(0)
  const [envReveal, setEnvReveal] = useState(false)
  // project picker (P): registry snapshot taken when it opens, what was typed, and the folder waiting for stop/leave
  const [projEntries, setProjEntries] = useState<ProjectEntry[]>([])
  const [projStatuses, setProjStatuses] = useState<ReadonlyMap<string, ProjectStatus>>(new Map())
  const [projQuery, setProjQuery] = useState("")
  const [projIndex, setProjIndex] = useState(0)
  const [projInputKey, setProjInputKey] = useState(0)
  const [pendingDir, setPendingDir] = useState<string | undefined>()
  const [toast, setToast] = useState<{ text: string; color: string } | undefined>()
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(undefined)

  const logService = view === "logs" && logScope === "all" ? undefined : selected
  /** the lines the focused log panel shows (after the filter) */
  const viewLines = () => filterLines(sup.logs.lines(logService), filter)

  const [sidebarDelta, setSidebarDelta] = useState(noSidebarDelta)
  const [detailH, setDetailH] = useState<number | undefined>()

  const defaultSidebarW =
    viewDef.compactSidebar ? Math.min(30, Math.max(22, Math.floor(width * 0.18))) : Math.min(52, Math.max(40, Math.floor(width * 0.3)))
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

  const logActions = useRef({ export: () => {}, copyAll: () => {} })
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

  const openProject = useCallback(
    async (dir: string, how: "stop" | "detach") => {
      if (!onOpenProject) return notify("switching projects is not available here", theme.yellow)
      // keys are ignored while the services are dealt with; a successful switch remounts the app
      setMode(how === "stop" ? "stopping" : "external")
      const error = await onOpenProject(dir, how)
      if (error) {
        setPendingDir(undefined)
        setMode("normal")
        notify(error, theme.red)
      }
    },
    [onOpenProject, notify],
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
          setSidebarDelta(noSidebarDelta())
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
      { id: "toggle-wrap", label: "Toggle log line wrap", hint: "w", run: () => setWrap((v) => !v) },
      { id: "export-logs", label: "Export visible logs to a file", hint: "E", run: () => logActions.current.export() },
      { id: "copy-logs", label: "Copy visible logs to the clipboard", hint: "Y", run: () => logActions.current.copyAll() },
      { id: "projects", label: "Open project…", hint: "P", run: openProjects },
      { id: "theme", label: "Change theme…", hint: "T", run: openThemePicker },
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
  const projRows = useMemo(
    () => (mode === "projects" ? projectRows(projEntries, projStatuses, projQuery, sup.config.root) : []),
    [mode, projEntries, projStatuses, projQuery, sup],
  )

  const moveSelection = (delta: number) => {
    if (!names.length) return
    setSelected((cur) => names[(names.indexOf(cur) + delta + names.length) % names.length]!)
  }

  function clearLogQueries() {
    clearTimeout(toastTimer.current)
    setToast(undefined) // a lingering "match 3/17" would keep hiding the key hints
    setFilter("")
    setSearch("")
    setMatch(undefined)
  }

  function exitCopy() {
    setCopy(undefined)
    setMode("normal")
  }

  function copyLines(lines: readonly import("../core/logs.ts").LogLine[], what: string) {
    if (!lines.length) return notify("nothing to copy", theme.yellow)
    const text = formatLines(lines, { time: showTime, prefix: !logService })
    void clipboard.copy(renderer, text).then((r) =>
      r.ok ? notify(`copied ${what} (${lines.length} line${lines.length === 1 ? "" : "s"}) via ${r.via}`, theme.green) : notify(r.error ?? "copy failed", theme.red),
    )
  }

  function exportLogs() {
    const lines = viewLines()
    if (!lines.length) return notify("no logs to export", theme.yellow)
    try {
      const dir = join(sup.stateDir, "exports")
      mkdirSync(dir, { recursive: true })
      const d = new Date()
      const p2 = (n: number) => String(n).padStart(2, "0")
      const stamp = `${d.getFullYear()}${p2(d.getMonth() + 1)}${p2(d.getDate())}-${p2(d.getHours())}${p2(d.getMinutes())}${p2(d.getSeconds())}`
      const file = join(dir, `${logService ?? "all"}-${stamp}.log`)
      writeFileSync(file, formatLines(lines, { time: true, prefix: !logService }) + "\n")
      notify(`exported ${lines.length} lines → ${file}`, theme.green)
      // the path is what you want next (open it, attach it, paste it in an issue)
      void clipboard.copy(renderer, file).then((r) => {
        if (r.ok) notify(`exported ${lines.length} lines → ${file} (path copied)`, theme.green)
      })
    } catch (err) {
      notify(`export failed: ${(err as Error).message}`, theme.red)
    }
  }

  /** n / N: move to the next (newer) / previous (older) line matching the search; the first jump goes to the newest. */
  function jumpMatch(dir: 1 | -1, fromEnd = false) {
    if (!search) return notify("no search: press /, tab, type, enter", theme.yellow)
    const lines = viewLines()
    const hit = matcher(search)
    const idxs = lines.flatMap((l, i) => (hit(l) ? [i] : []))
    if (!idxs.length) return notify(`no matches for /${search}`, theme.yellow)
    const at = match === undefined || fromEnd ? -1 : lines.findIndex((l) => l.seq === match)
    let k = idxs.length - 1
    if (at >= 0) {
      const after = idxs.findIndex((i) => i > at)
      const before = idxs.filter((i) => i < at).length - 1
      k = dir > 0 ? (after < 0 ? 0 : after) : before < 0 ? idxs.length - 1 : before
    }
    setMatch(lines[idxs[k]!]!.seq)
    notify(`match ${k + 1}/${idxs.length}`, theme.accent)
  }

  logActions.current = { export: exportLogs, copyAll: () => copyLines(viewLines(), "all logs in view") }

  useKeyboard((key: KeyEvent) => {
    const ch = key.sequence
    const page = Math.max(5, height - (zoomed ? 4 : view === "dashboard" ? detailRows + 3 : 5))
    const logPage = page
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
    if (mode === "switch") {
      if (!pendingDir) return setMode("normal")
      if (ch === "y" || ch === "Y" || ch === "s" || ch === "S" || key.name === "return") void openProject(pendingDir, "stop")
      else if (ch === "d" || ch === "D") void openProject(pendingDir, "detach")
      else if (ch === "n" || key.name === "escape" || ch === "q") {
        setPendingDir(undefined)
        setMode("normal")
      }
      return
    }
    if (mode === "projects") {
      const row = projRows[projIndex]
      if (key.name === "escape") return setMode("normal")
      if (key.name === "up" || (key.ctrl && key.name === "p")) return setProjIndex((i) => Math.max(0, i - 1))
      if (key.name === "down" || (key.ctrl && key.name === "n")) return setProjIndex((i) => Math.min(projRows.length - 1, i + 1))
      if (key.name === "tab") {
        if (looksLikePath(projQuery)) {
          setProjQuery(completePath(projQuery))
          setProjInputKey((k) => k + 1)
        }
        return
      }
      if (key.ctrl && key.name === "f" && row?.entry) {
        setPinned(row.entry.path, !row.entry.pinned)
        return setProjEntries(readProjects())
      }
      if (key.ctrl && key.name === "x" && row?.entry) {
        forgetProject(row.entry.path)
        return setProjEntries(readProjects())
      }
      if (key.name === "return" && row) {
        if (row.missing) return notify(`${row.path} is not a folder`, theme.red)
        setPendingDir(row.path)
        if (sup.ownedRunningCount() === 0) return void openProject(row.path, "stop")
        return setMode("switch")
      }
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
    if (mode === "theme") {
      if (key.name === "escape" || ch === "q") return cancelTheme()
      if (key.name === "return") return saveTheme()
      if (key.name === "up" || ch === "k" || (key.ctrl && key.name === "p")) return previewTheme(themeIndex - 1)
      if (key.name === "down" || ch === "j" || (key.ctrl && key.name === "n")) return previewTheme(themeIndex + 1)
      return
    }
    if (mode === "filter") {
      if (key.name === "escape") {
        clearLogQueries()
        setMode("normal")
      } else if (key.name === "return") {
        setMode("normal")
        // a new search starts at its newest match
        if (barKind === "search" && search) jumpMatch(1, true)
      } else if (key.name === "tab") {
        // the text typed so far moves over to the other kind of bar
        const text = barKind === "filter" ? filter : search
        setFilter(barKind === "filter" ? "" : text)
        setSearch(barKind === "filter" ? text : "")
        setMatch(undefined)
        setBarKind(barKind === "filter" ? "search" : "filter")
      }
      return
    }
    if (mode === "copy") {
      const lines = viewLines()
      if (!lines.length || !copy) return exitCopy()
      const at = Math.max(0, lines.findIndex((l) => l.seq === copy.cursor))
      const half = Math.max(2, Math.floor(logPage / 2))
      const go = (i: number) => setCopy({ ...copy, cursor: lines[Math.max(0, Math.min(lines.length - 1, i))]!.seq })
      if (key.name === "escape" || ch === "q") return exitCopy()
      if (key.name === "down" || ch === "j") return go(at + 1)
      if (key.name === "up" || ch === "k") return go(at - 1)
      if (key.ctrl && key.name === "d") return go(at + half)
      if (key.ctrl && key.name === "u") return go(at - half)
      if (key.name === "pagedown") return go(at + logPage)
      if (key.name === "pageup") return go(at - logPage)
      if (ch === "g" || key.name === "home") return go(0)
      if (ch === "G" || key.name === "end") return go(lines.length - 1)
      if (ch === "v" || ch === " ") return setCopy({ cursor: copy.cursor, anchor: copy.anchor === undefined ? copy.cursor : undefined })
      if (ch === "y" || key.name === "return") {
        const from = copy.anchor === undefined ? at : Math.max(0, lines.findIndex((l) => l.seq === copy.anchor))
        const range = lines.slice(Math.min(at, from), Math.max(at, from) + 1)
        exitCopy()
        return copyLines(range, "selection")
      }
      return
    }

    // ---- normal mode
    if (viewCapture.current) {
      // a text prompt inside the view is open: it takes every key, except the way out
      if (key.ctrl && key.name === "c") return requestQuit()
      viewKeys.current?.(key)
      return
    }
    if (key.ctrl && key.name === "c") return requestQuit()
    if (key.ctrl && key.name === "p") return openPalette()
    if (ch === "q") return requestQuit()
    if (ch === ":") return openPalette()
    if (ch === "?") return setMode("help")
    if (ch === "T") return openThemePicker()
    if (ch === "P") return openProjects()
    const byNumber = /^[1-9]$/.test(ch) ? VIEWS[Number(ch) - 1] : undefined
    if (byNumber) return setView(byNumber.id)
    if (key.name === "tab") {
      const panes = viewDef.visiblePanes?.(viewContext) ?? viewDef.panes
      const i = Math.max(0, panes.indexOf(focus))
      return setFocus(panes[(i + (key.shift ? panes.length - 1 : 1)) % panes.length]!)
    }
    if (ch === "z") return setZoomed((v) => !v)
    if (ch === "+") return resize(1)
    if (ch === "-") return resize(-1)
    if (ch === "=") return resetSize()
    if (key.name === "escape" && (search || filter)) return clearLogQueries()
    if (key.name === "escape" && zoomed) return setZoomed(false)
    if (viewKeys.current?.(key)) return

    if (focus === "logs") {
      if (key.name === "down" || ch === "j") return setScrollBack((v) => Math.max(0, v - 1))
      if (key.name === "up" || ch === "k") return setScrollBack((v) => v + 1)
      if (key.ctrl && key.name === "d") return setScrollBack((v) => Math.max(0, v - Math.floor(page / 2)))
      if (key.ctrl && key.name === "u") return setScrollBack((v) => v + Math.floor(page / 2))
      if (ch === "g" || key.name === "home") return setScrollBack(Number.MAX_SAFE_INTEGER)
      if (ch === "G" || key.name === "end") return setScrollBack(0)
      if (ch === "v") {
        const lines = viewLines()
        const last = lines[Math.max(0, lines.length - 1 - scrollBack)]
        if (!last) return notify("no logs to select", theme.yellow)
        setCopy({ cursor: last.seq })
        return setMode("copy")
      }
    }

    if (view === "graph" && focus === "graph" && ["up", "down", "left", "right"].includes(key.name)) {
      const dir = key.name as "up" | "down" | "left" | "right"
      return setSelected((cur) => neighbourInDirection(layout, cur, dir) ?? cur)
    }
    if (key.name === "down" || ch === "j") return moveSelection(1)
    if (key.name === "up" || ch === "k") return moveSelection(-1)
    if (ch === "g" || key.name === "home") return setSelected(names[0] ?? "")
    if (ch === "G" || key.name === "end") return setSelected(names[names.length - 1] ?? "")

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
    if (ch === "w") return setWrap((v) => !v)
    if (ch === "Y") return copyLines(viewLines(), "all logs in view")
    if (ch === "E") return exportLogs()
    if (ch === "n") return jumpMatch(1)
    if (ch === "N") return jumpMatch(-1)
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

  const themeEntries = useMemo(
    () => Object.entries(themes).map(([name, palette]) => ({ name, palette, custom: customThemes.includes(name) })),
    [themes, customThemes],
  )

  function previewTheme(index: number) {
    const entry = themeEntries[Math.max(0, Math.min(themeEntries.length - 1, index))]
    if (!entry) return
    setThemeIndex(themeEntries.indexOf(entry))
    applyTheme(entry.palette)
    setThemeName(entry.name)
  }

  function openThemePicker() {
    setThemeIndex(Math.max(0, themeEntries.findIndex((e) => e.name === themeName)))
    setMode("theme")
  }

  function saveTheme() {
    savedTheme.current = themeName
    setMode("normal")
    if (writeUserConfig({ theme: themeName })) notify(`theme: ${themeName}`, theme.accent)
    else notify(`theme: ${themeName} (could not save it to the config file)`, theme.yellow)
  }

  function cancelTheme() {
    const saved = themeEntries.find((e) => e.name === savedTheme.current)
    if (saved) {
      applyTheme(saved.palette)
      setThemeName(saved.name)
    }
    setMode("normal")
  }

  useEffect(() => {
    if (startWithPicker) openProjects()
  }, [])

  useEffect(() => {
    if (themeErrors.length) notify(`${themeErrors[0]}${themeErrors.length > 1 ? ` (+${themeErrors.length - 1} more)` : ""}`, theme.yellow)
  }, [])

  function openProjects() {
    const entries = readProjects()
    setProjEntries(entries)
    setProjStatuses(new Map(entries.map((e) => [e.path, projectStatus(e)])))
    setProjQuery("")
    setProjIndex(0)
    setProjInputKey((k) => k + 1)
    setMode("projects")
  }

  function openPalette() {
    setQuery("")
    setPaletteIndex(0)
    setMode("palette")
  }

  // branch and change count for the header: a cheap `git status` every few seconds (App re-renders on its own tick)
  useEffect(() => {
    if (!repos.length) return
    const refresh = () => repos.forEach((e) => void e.repo.refresh())
    refresh()
    const timer = setInterval(refresh, 5000)
    return () => clearInterval(timer)
  }, [repos])
  const gitSummary = summarize(repos)
  const onlyRepo = repos.length === 1 ? repos[0]!.repo.status : undefined

  useEffect(() => setPaletteIndex(0), [query])
  useEffect(() => setProjIndex(0), [projQuery])
  useEffect(() => setScrollBack(0), [selected, logScope, view])
  useEffect(() => {
    // the Git view opens on the repo of the service you were looking at
    if (view === "git") {
      const i = repoOfService(repos, selected)
      if (i >= 0) setRepoIndex(i)
    }
    setFocus(viewById(view).defaultPane)
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

  const logExtras = {
    cursor: mode === "copy" ? copy?.cursor : undefined,
    anchor: mode === "copy" ? copy?.anchor : undefined,
    freeze: mode === "copy",
    search: search || undefined,
    current: match,
    onSelect: (anchor: number, cursor: number) => {
      setCopy({ cursor, anchor: anchor === cursor ? undefined : anchor })
      setMode("copy")
    },
  }
  const viewContext: ViewContext = {
    sup,
    names,
    selected,
    setSelected,
    tick,
    width,
    height,
    sidebarW,
    detailRows,
    zoomed,
    focus,
    setFocus,
    notify,
    repos,
    repoIndex,
    setRepoIndex,
    keys: viewKeys,
    setHints: setViewHints,
    capture: viewCapture,
    logs: {
      service: logService,
      scope: logScope,
      lines: (service) => sup.logs.lines(service),
      filter,
      scrollBack,
      onScroll: (d) => setScrollBack((v) => Math.max(0, v + d)),
      showTime,
      wrap,
      extras: logExtras,
    },
  }

  const footerHints: string[][] =
    mode === "copy"
      ? [
          ["j/k", "move"],
          ["ctrl+d/u", "page"],
          ["v", "start/clear selection"],
          ["y", "copy"],
          ["esc", "cancel"],
        ]
      : (viewHints ?? viewDef.hints?.(viewContext) ?? [
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
        ])

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
        {repos.length ? (
          <text>
            <span fg={theme.accent2}>
              {onlyRepo ? ` ⎇ ${onlyRepo.branch ?? `detached ${onlyRepo.oid ?? ""}`}` : repos.length > 1 ? ` ⎇ ${repos.length} repos` : " ⎇ …"}
            </span>
            {gitSummary.ahead ? <span fg={theme.yellow}>{` ↑${gitSummary.ahead}`}</span> : null}
            {gitSummary.behind ? <span fg={theme.yellow}>{` ↓${gitSummary.behind}`}</span> : null}
            {gitSummary.changes ? <span fg={theme.orange}>{` ✎${gitSummary.changes}${repos.length > 1 ? ` in ${gitSummary.dirty}` : ""}`}</span> : null}
            <span fg={theme.dim}>{"   "}</span>
          </text>
        ) : null}
        <text>
          <span fg={theme.green}>● {counts.up} up</span>
          {counts.busy ? <span fg={theme.yellow}>{`  ◐ ${counts.busy}`}</span> : null}
          {counts.bad ? <span fg={theme.red}>{`  ✖ ${counts.bad}`}</span> : null}
          <span fg={theme.dim}>{`  ○ ${names.length - counts.up - counts.bad - counts.busy}`}</span>
        </text>
      </box>

      {/* body */}
      <box flexGrow={1} flexDirection="row">
        {viewDef.panes.includes("services") && (!zoomed || focus === "services") ? (
          <ServiceList
            sup={sup}
            names={names}
            selected={selected}
            onSelect={setSelected}
            tick={tick}
            width={zoomed ? width : sidebarW}
            focused={focus === "services"}
            compact={(!!viewDef.compactSidebar || sidebarW < 36) && !zoomed}
            onFocus={() => setFocus("services")}
          />
        ) : null}
        {!zoomed || focus !== "services" ? (
          <box flexGrow={1} flexDirection="column">
            {viewDef.render(viewContext)}
          </box>
        ) : null}
      </box>

      {/* filter bar */}
      {mode === "filter" ? (
        <box height={1} flexDirection="row" paddingLeft={1} backgroundColor={theme.panelAlt}>
          <text fg={theme.accent}>{barKind === "filter" ? "filter " : "search "}</text>
          <input
            key={barKind}
            flexGrow={1}
            focused
            value={barKind === "filter" ? filter : search}
            placeholder={
              barKind === "filter"
                ? "hide lines not matching (regex) · tab: search instead · enter keep · esc clear"
                : "highlight matches (regex), n/N to jump · tab: filter instead · enter go · esc clear"
            }
            onInput={barKind === "filter" ? setFilter : setSearch}
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
            {footerHints.flatMap(([k, v]) => [
              <span key={`k${k}`} fg={theme.accent}>
                {k}
              </span>,
              <span key={`v${k}`} fg={theme.dim}>{` ${v}   `}</span>,
            ])}
          </text>
        )}
        <box flexGrow={1} />
        {selected && viewDef.panes.includes("services") ? (
          <text>
            <span fg={statusStyle[sup.state(selected).status].color}>{statusStyle[sup.state(selected).status].icon} </span>
            <span fg={theme.muted}>{selected}</span>
          </text>
        ) : null}
      </box>

      {mode === "theme" ? <ThemePicker entries={themeEntries} selected={themeIndex} current={savedTheme.current} width={width} height={height} /> : null}
      {mode === "palette" ? <CommandPalette commands={matches} selected={paletteIndex} onQuery={setQuery} width={width} /> : null}
      {mode === "projects" ? (
        <ProjectPicker rows={projRows} selected={projIndex} value={projQuery} inputKey={projInputKey} onQuery={setProjQuery} width={width} height={height} />
      ) : null}
      {mode === "help" ? <HelpOverlay width={width} height={height} /> : null}
      {mode === "env" && selected ? (
        <EnvOverlay service={selected} {...resolveEnv(sup.service(selected), sup.config.root)} scroll={envScroll} reveal={envReveal} width={width} height={height} />
      ) : null}
      {mode === "quit" || mode === "switch" || mode === "stopping" ? (
        <ConfirmOverlay
          width={width}
          action={pendingDir ? "switch" : "quit"}
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
