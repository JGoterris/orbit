import type { KeyEvent } from "@opentui/core"
import type { ComponentProps, MutableRefObject, ReactNode } from "react"
import type { LogLine } from "../../core/logs.ts"
import type { RepoEntry } from "../../core/git/repos.ts"
import type { SupervisorLike } from "../../core/supervisor.ts"
import type { LogView } from "../LogView.tsx"

/** Every focusable panel any view can show. Views list the ones they use in `ViewDef.panes`. */
export type Pane = "services" | "detail" | "logs" | "graph" | "repos" | "changes" | "branches" | "commits" | "stash" | "gitdiff"

/** Returns true when it used the key (the shell then does not). */
export type KeyHandler = (key: KeyEvent) => boolean

/** What the shell hands to a view so it can render its body. */
export interface ViewContext {
  sup: SupervisorLike
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
  notify: (text: string, color?: string) => void
  /** the git repositories the project's services live in (possibly none, possibly several) */
  repos: RepoEntry[]
  /** which of `repos` the Git view is working on */
  repoIndex: number
  setRepoIndex: (index: number) => void
  /**
   * A view that wants keys sets `keys.current` while it renders. The shell offers it every key that no
   * global shortcut (quit, views, tab, palette…) took, before the service shortcuts.
   */
  keys: MutableRefObject<KeyHandler | undefined>
  /** Replaces the footer key hints (e.g. while a mode is active); `undefined` goes back to `ViewDef.hints`. */
  setHints: (hints: string[][] | undefined) => void
  /** While a view sets this (a text prompt is open) it gets *every* key and the shell takes none. */
  capture: MutableRefObject<boolean>
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
    extras: Pick<ComponentProps<typeof LogView>, "cursor" | "anchor" | "freeze" | "search" | "current" | "onSelect" | "fold" | "expanded" | "onToggleTrace">
  }
}

export interface ViewDef {
  id: string
  label: string
  /** panels in `tab` order; the shell draws the service sidebar when it contains "services" */
  panes: Pane[]
  defaultPane: Pane
  /** the panels that are actually on screen right now, when that depends on the data (default: `panes`) */
  visiblePanes?(ctx: ViewContext): Pane[]
  /** narrow sidebar (the view needs the room) */
  compactSidebar?: boolean
  render(ctx: ViewContext): ReactNode
  /** footer key hints, `[key, what]` pairs; default is the service shortcuts */
  hints?(ctx: ViewContext): string[][]
}
