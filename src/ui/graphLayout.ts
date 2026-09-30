/**
 * Layered layout of the dependency graph, painted onto a character grid.
 *
 *   columns = dependency levels (level 0 = no dependencies, on the left)
 *   edges   = dependency -> dependent (the order in which things start)
 *
 * Edges that span more than one level go through dummy nodes so they never cross a box.
 */
import { levels, type DepMap } from "../core/graph.ts"

export interface NodeBox {
  name: string
  x: number
  y: number
  w: number
  h: number
  col: number
}

interface LNode {
  id: string
  real: boolean
  col: number
  /** for dummies: the edge key they belong to */
  edge?: string
}

interface Segment {
  from: string
  to: string
  edge: string // "dep>dependent"
}

export interface GraphLayout {
  width: number
  height: number
  boxes: NodeBox[]
  /** grid of direction masks for edges, plus which edges pass through each cell */
  masks: Uint8Array
  cellEdges: Map<number, Set<string>>
  arrows: Map<number, Set<string>>
  /** cells on a box border where an edge leaves: rendered as ├ */
  ports: Map<number, Set<string>>
}

const N = 1
const E = 2
const S = 4
const W = 8

export const BOX_H = 4
const ROW_H = BOX_H + 1
const MIN_BOX_W = 22
const MAX_BOX_W = 26

export function edgeKey(dep: string, dependent: string) {
  return `${dep}>${dependent}`
}

export function layoutGraph(deps: DepMap, order: readonly string[] = Object.keys(deps)): GraphLayout {
  const lv = levels(deps)
  const maxLevel = Math.max(0, ...Object.values(lv))
  const layers: LNode[][] = Array.from({ length: maxLevel + 1 }, () => [])
  const nodes = new Map<string, LNode>()
  const segments: Segment[] = []

  for (const name of order) {
    const n: LNode = { id: name, real: true, col: lv[name] ?? 0 }
    nodes.set(name, n)
    layers[n.col]!.push(n)
  }
  // edges, with dummies for long spans
  for (const name of order) {
    for (const dep of deps[name] ?? []) {
      const key = edgeKey(dep, name)
      let prev = dep
      for (let c = (lv[dep] ?? 0) + 1; c < (lv[name] ?? 0); c++) {
        const id = `~${key}@${c}`
        const d: LNode = { id, real: false, col: c, edge: key }
        nodes.set(id, d)
        layers[c]!.push(d)
        segments.push({ from: prev, to: id, edge: key })
        prev = id
      }
      segments.push({ from: prev, to: name, edge: key })
    }
  }

  // --- crossing reduction: barycenter sweeps
  const preds = new Map<string, string[]>()
  const succs = new Map<string, string[]>()
  for (const s of segments) {
    ;(preds.get(s.to) ?? preds.set(s.to, []).get(s.to)!).push(s.from)
    ;(succs.get(s.from) ?? succs.set(s.from, []).get(s.from)!).push(s.to)
  }
  const pos = new Map<string, number>()
  const reindex = (layer: LNode[]) => layer.forEach((n, i) => pos.set(n.id, i))
  layers.forEach(reindex)
  const sortBy = (layer: LNode[], neighbours: Map<string, string[]>) => {
    const bary = new Map<string, number>()
    for (const n of layer) {
      const ns = neighbours.get(n.id) ?? []
      const isolated = !preds.has(n.id) && !succs.has(n.id)
      // isolated services sink to the bottom so they don't push connected ones apart
      bary.set(n.id, isolated ? 1e6 + pos.get(n.id)! : ns.length ? ns.reduce((a, id) => a + pos.get(id)!, 0) / ns.length : pos.get(n.id)!)
    }
    layer.sort((a, b) => bary.get(a.id)! - bary.get(b.id)! || pos.get(a.id)! - pos.get(b.id)!)
    reindex(layer)
  }
  for (let iter = 0; iter < 6; iter++) {
    for (let c = 1; c <= maxLevel; c++) sortBy(layers[c]!, preds)
    for (let c = maxLevel - 1; c >= 0; c--) sortBy(layers[c]!, succs)
  }

  // --- geometry
  const colW = layers.map((layer) =>
    Math.min(MAX_BOX_W, Math.max(MIN_BOX_W, ...layer.filter((n) => n.real).map((n) => n.id.length + 6))),
  )
  // lanes: one vertical track per source node in each gap that needs to bend
  const maxSlots = Math.max(1, ...layers.map((l) => l.length))
  const slotY = (n: LNode) => {
    const layer = layers[n.col]!
    const offset = Math.floor(((maxSlots - layer.length) * ROW_H) / 2)
    return offset + pos.get(n.id)! * ROW_H
  }
  const anchorY = (id: string) => slotY(nodes.get(id)!) + 1

  const gapSources: string[][] = layers.map(() => [])
  const bending = segments.filter((s) => anchorY(s.from) !== anchorY(s.to))
  for (const s of bending) {
    const col = nodes.get(s.from)!.col
    if (!gapSources[col]!.includes(s.from)) gapSources[col]!.push(s.from)
  }
  gapSources.forEach((srcs, col) => {
    srcs.sort((a, b) => anchorY(a) - anchorY(b))
    // A source whose row is where another source's edge *arrives* must use a lane to the left of
    // that other source's lane, otherwise both horizontal pieces overlap and look connected.
    const before = new Map<string, Set<string>>(srcs.map((s) => [s, new Set()]))
    for (const seg of bending) {
      if (nodes.get(seg.from)!.col !== col) continue
      for (const other of srcs) {
        if (other !== seg.from && anchorY(other) === anchorY(seg.to)) before.get(seg.from)!.add(other)
      }
    }
    const ordered: string[] = []
    const state = new Map<string, 1 | 2>()
    const visit = (s: string) => {
      if (state.has(s)) return // done, or a cycle we cannot satisfy anyway
      state.set(s, 1)
      for (const b of before.get(s)!) visit(b)
      state.set(s, 2)
      ordered.push(s)
    }
    srcs.forEach(visit)
    srcs.splice(0, srcs.length, ...ordered)
  })
  const gapW = gapSources.map((srcs, c) => (c === maxLevel ? 0 : Math.max(6, srcs.length * 2 + 4)))

  const colX: number[] = []
  let x = 1
  for (let c = 0; c <= maxLevel; c++) {
    colX.push(x)
    x += colW[c]! + gapW[c]!
  }
  const width = x + 1
  const height = maxSlots * ROW_H

  const masks = new Uint8Array(width * height)
  const cellEdges = new Map<number, Set<string>>()
  const arrows = new Map<number, Set<string>>()
  const ports = new Map<number, Set<string>>()
  const idx = (cx: number, cy: number) => cy * width + cx
  const mark = (cx: number, cy: number, m: number, edge: string) => {
    if (cx < 0 || cy < 0 || cx >= width || cy >= height) return
    const i = idx(cx, cy)
    masks[i]! |= m
    ;(cellEdges.get(i) ?? cellEdges.set(i, new Set()).get(i)!).add(edge)
  }
  const hline = (x0: number, x1: number, cy: number, edge: string) => {
    const [a, b] = x0 <= x1 ? [x0, x1] : [x1, x0]
    for (let cx = a; cx < b; cx++) {
      mark(cx, cy, E, edge)
      mark(cx + 1, cy, W, edge)
    }
    if (a === b) mark(a, cy, 0, edge)
  }
  const vline = (cx: number, y0: number, y1: number, edge: string) => {
    const [a, b] = y0 <= y1 ? [y0, y1] : [y1, y0]
    for (let cy = a; cy < b; cy++) {
      mark(cx, cy, S, edge)
      mark(cx, cy + 1, N, edge)
    }
  }

  for (const s of segments) {
    const from = nodes.get(s.from)!
    const to = nodes.get(s.to)!
    const sx = colX[from.col]! + colW[from.col]!
    const tx = colX[to.col]! - 1
    const sy = anchorY(s.from)
    const ty = anchorY(s.to)
    // stub coming out of the box border
    mark(sx, sy, W, s.edge)
    if (from.real) {
      const p = idx(sx - 1, sy)
      ;(ports.get(p) ?? ports.set(p, new Set()).get(p)!).add(s.edge)
    }
    if (sy === ty) {
      hline(sx, tx, sy, s.edge)
    } else {
      const lane = gapSources[from.col]!.indexOf(s.from)
      const lx = sx + 2 + lane * 2
      hline(sx, lx, sy, s.edge)
      vline(lx, sy, ty, s.edge)
      hline(lx, tx, ty, s.edge)
    }
    if (to.real) {
      const i = idx(tx, ty)
      ;(arrows.get(i) ?? arrows.set(i, new Set()).get(i)!).add(s.edge)
    } else {
      // dummy: straight line across its column
      hline(tx, colX[to.col]! + colW[to.col]!, ty, s.edge)
    }
  }

  const boxes: NodeBox[] = [...nodes.values()]
    .filter((n) => n.real)
    .map((n) => ({ name: n.id, x: colX[n.col]!, y: slotY(n), w: colW[n.col]!, h: BOX_H, col: n.col }))

  return { width, height, boxes, masks, cellEdges, arrows, ports }
}

// ------------------------------------------------------------------ painting

export interface Cell {
  ch: string
  fg: string
  bg?: string
  bold?: boolean
}

export interface Run {
  text: string
  fg: string
  bg?: string
  bold?: boolean
}

export interface NodeStyle {
  icon: string
  iconColor: string
  title: string
  subtitle: string
  subtitleColor: string
  border: string
  selected?: boolean
}

export interface PaintOptions {
  node: (name: string) => NodeStyle
  edgeColor: (edge: string) => string | undefined
  defaultEdge: string
  text: string
}

const LINE_CHARS: Record<number, string> = {
  [E]: "─",
  [W]: "─",
  [E | W]: "─",
  [N]: "│",
  [S]: "│",
  [N | S]: "│",
  [E | S]: "╭",
  [W | S]: "╮",
  [N | E]: "╰",
  [N | W]: "╯",
  [N | S | E]: "├",
  [N | S | W]: "┤",
  [E | W | S]: "┬",
  [E | W | N]: "┴",
  [N | E | S | W]: "┼",
}

function truncate(s: string, w: number) {
  return s.length <= w ? s : s.slice(0, Math.max(0, w - 1)) + "…"
}

export function paintGraph(layout: GraphLayout, opts: PaintOptions): Cell[][] {
  const { width, height } = layout
  const grid: Cell[][] = Array.from({ length: height }, () =>
    Array.from({ length: width }, () => ({ ch: " ", fg: opts.text })),
  )
  const pickColor = (edges: Set<string> | undefined) => {
    if (!edges) return opts.defaultEdge
    for (const e of edges) {
      const c = opts.edgeColor(e)
      if (c) return c
    }
    return opts.defaultEdge
  }

  for (let i = 0; i < layout.masks.length; i++) {
    const m = layout.masks[i]!
    if (!m) continue
    const cx = i % width
    const cy = Math.floor(i / width)
    grid[cy]![cx] = { ch: LINE_CHARS[m] ?? "·", fg: pickColor(layout.cellEdges.get(i)) }
  }
  for (const [i, edges] of layout.arrows) {
    const c = pickColor(edges)
    grid[Math.floor(i / width)]![i % width] = { ch: "▶", fg: c, bold: true }
  }

  for (const b of layout.boxes) {
    const st = opts.node(b.name)
    const [tl, tr, bl, br, h, v] = st.selected ? ["╔", "╗", "╚", "╝", "═", "║"] : ["╭", "╮", "╰", "╯", "─", "│"]
    const put = (cx: number, cy: number, cell: Cell) => {
      if (cy >= 0 && cy < height && cx >= 0 && cx < width) grid[cy]![cx] = cell
    }
    const border = { fg: st.border, bold: st.selected }
    put(b.x, b.y, { ch: tl!, ...border })
    put(b.x + b.w - 1, b.y, { ch: tr!, ...border })
    put(b.x, b.y + b.h - 1, { ch: bl!, ...border })
    put(b.x + b.w - 1, b.y + b.h - 1, { ch: br!, ...border })
    for (let cx = b.x + 1; cx < b.x + b.w - 1; cx++) {
      put(cx, b.y, { ch: h!, ...border })
      put(cx, b.y + b.h - 1, { ch: h!, ...border })
    }
    for (let cy = b.y + 1; cy < b.y + b.h - 1; cy++) {
      put(b.x, cy, { ch: v!, ...border })
      put(b.x + b.w - 1, cy, { ch: v!, ...border })
      for (let cx = b.x + 1; cx < b.x + b.w - 1; cx++) put(cx, cy, { ch: " ", fg: opts.text })
    }
    const inner = b.w - 4
    put(b.x + 2, b.y + 1, { ch: st.icon, fg: st.iconColor, bold: true })
    const title = truncate(st.title, inner - 2)
    ;[...title].forEach((ch, i) => put(b.x + 4 + i, b.y + 1, { ch, fg: opts.text, bold: true }))
    const sub = truncate(st.subtitle, inner)
    ;[...sub].forEach((ch, i) => put(b.x + 2 + i, b.y + 2, { ch, fg: st.subtitleColor }))
  }
  for (const [i, edges] of layout.ports) {
    const cy = Math.floor(i / width)
    const cx = i % width
    const cell = grid[cy]![cx]!
    if (cell.ch !== "│" && cell.ch !== "║") continue
    const highlight = [...edges].map(opts.edgeColor).find(Boolean)
    grid[cy]![cx] = { ...cell, ch: cell.ch === "│" ? "├" : "╟", fg: highlight ?? cell.fg }
  }
  return grid
}

/** Collapses a row of cells into styled runs (fewer spans to render). */
export function toRuns(row: Cell[], from = 0, to = row.length): Run[] {
  const runs: Run[] = []
  for (let i = Math.max(0, from); i < Math.min(to, row.length); i++) {
    const c = row[i]!
    const last = runs[runs.length - 1]
    if (last && last.fg === c.fg && last.bg === c.bg && !!last.bold === !!c.bold) last.text += c.ch
    else runs.push({ text: c.ch, fg: c.fg, bg: c.bg, bold: c.bold })
  }
  return runs
}

export function gridToString(grid: Cell[][]): string {
  return grid.map((r) => r.map((c) => c.ch).join("").trimEnd()).join("\n")
}
