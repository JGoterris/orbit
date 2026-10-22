// Package graphlayout lays the dependency graph out in layers and paints it onto a character grid.
//
//	columns = dependency levels (level 0 = no dependencies, on the left)
//	edges   = dependency -> dependent (the order in which things start)
//
// Edges that span more than one level go through dummy nodes so they never cross a box.
package graphlayout

import (
	"sort"
	"strconv"

	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/graph"
)

type NodeBox struct {
	Name string
	X, Y int
	W, H int
	Col  int
}

type lnode struct {
	id   string
	real bool
	col  int
}

type segment struct {
	from, to string
	edge     string // "dep>dependent"
}

// edgeSet is an insertion-ordered set of edge keys.
type edgeSet []string

func (s edgeSet) has(e string) bool {
	for _, x := range s {
		if x == e {
			return true
		}
	}
	return false
}

type cellSets map[int]edgeSet

func (c cellSets) add(i int, e string) {
	if !c[i].has(e) {
		c[i] = append(c[i], e)
	}
}

// Layout is the geometry of the graph, ready to paint.
type Layout struct {
	Width, Height int
	Boxes         []NodeBox
	// masks is a grid of direction masks for the edges; cellEdges says which edges pass through each cell
	masks     []uint8
	cellEdges cellSets
	arrows    cellSets
	// ports are the cells on a box border where an edge leaves: rendered as ├
	ports cellSets
}

const (
	dirN uint8 = 1
	dirE uint8 = 2
	dirS uint8 = 4
	dirW uint8 = 8
)

const (
	BoxH     = 4
	rowH     = BoxH + 1
	minBoxW  = 22
	maxBoxW  = 26
	maxSweep = 6
)

// EdgeKey names the edge dep -> dependent.
func EdgeKey(dep, dependent string) string { return dep + ">" + dependent }

func nameWidth(s string) int { return ansi.StringWidth(s) }

// LayoutGraph places the services of deps in layers; order is the declaration order, which breaks ties.
func LayoutGraph(deps graph.DepMap, order []string) *Layout {
	if order == nil {
		order = deps.Names
	}
	lv := graph.Levels(deps)
	maxLevel := 0
	for _, l := range lv {
		maxLevel = max(maxLevel, l)
	}
	layers := make([][]*lnode, maxLevel+1)
	nodes := map[string]*lnode{}
	var segments []segment

	for _, name := range order {
		n := &lnode{id: name, real: true, col: lv[name]}
		nodes[name] = n
		layers[n.col] = append(layers[n.col], n)
	}
	// edges, with dummies for long spans
	for _, name := range order {
		for _, dep := range deps.Deps[name] {
			key := EdgeKey(dep, name)
			prev := dep
			for c := lv[dep] + 1; c < lv[name]; c++ {
				id := "~" + key + "@" + strconv.Itoa(c)
				d := &lnode{id: id, col: c}
				nodes[id] = d
				layers[c] = append(layers[c], d)
				segments = append(segments, segment{from: prev, to: id, edge: key})
				prev = id
			}
			segments = append(segments, segment{from: prev, to: name, edge: key})
		}
	}

	// --- crossing reduction: barycenter sweeps
	preds := map[string][]string{}
	succs := map[string][]string{}
	for _, s := range segments {
		preds[s.to] = append(preds[s.to], s.from)
		succs[s.from] = append(succs[s.from], s.to)
	}
	pos := map[string]int{}
	reindex := func(layer []*lnode) {
		for i, n := range layer {
			pos[n.id] = i
		}
	}
	for _, l := range layers {
		reindex(l)
	}
	sortBy := func(layer []*lnode, neighbours map[string][]string) {
		bary := map[string]float64{}
		for _, n := range layer {
			ns := neighbours[n.id]
			_, hasPred := preds[n.id]
			_, hasSucc := succs[n.id]
			switch {
			case !hasPred && !hasSucc:
				// isolated services sink to the bottom so they don't push connected ones apart
				bary[n.id] = 1e6 + float64(pos[n.id])
			case len(ns) > 0:
				sum := 0.0
				for _, id := range ns {
					sum += float64(pos[id])
				}
				bary[n.id] = sum / float64(len(ns))
			default:
				bary[n.id] = float64(pos[n.id])
			}
		}
		sort.SliceStable(layer, func(i, j int) bool {
			a, b := layer[i], layer[j]
			if bary[a.id] != bary[b.id] {
				return bary[a.id] < bary[b.id]
			}
			return pos[a.id] < pos[b.id]
		})
		reindex(layer)
	}
	for iter := 0; iter < maxSweep; iter++ {
		for c := 1; c <= maxLevel; c++ {
			sortBy(layers[c], preds)
		}
		for c := maxLevel - 1; c >= 0; c-- {
			sortBy(layers[c], succs)
		}
	}

	// --- geometry
	colW := make([]int, len(layers))
	for c, layer := range layers {
		w := minBoxW
		for _, n := range layer {
			if n.real {
				w = max(w, nameWidth(n.id)+6)
			}
		}
		colW[c] = min(maxBoxW, w)
	}
	maxSlots := 1
	for _, l := range layers {
		maxSlots = max(maxSlots, len(l))
	}
	slotY := func(n *lnode) int {
		layer := layers[n.col]
		offset := ((maxSlots - len(layer)) * rowH) / 2
		return offset + pos[n.id]*rowH
	}
	anchorY := func(id string) int { return slotY(nodes[id]) + 1 }

	// lanes: one vertical track per source node in each gap that needs to bend
	gapSources := make([][]string, len(layers))
	var bending []segment
	for _, s := range segments {
		if anchorY(s.from) != anchorY(s.to) {
			bending = append(bending, s)
		}
	}
	for _, s := range bending {
		col := nodes[s.from].col
		found := false
		for _, x := range gapSources[col] {
			if x == s.from {
				found = true
			}
		}
		if !found {
			gapSources[col] = append(gapSources[col], s.from)
		}
	}
	for col, srcs := range gapSources {
		sort.SliceStable(srcs, func(i, j int) bool { return anchorY(srcs[i]) < anchorY(srcs[j]) })
		// A source whose row is where another source's edge *arrives* must use a lane to the left of that other
		// source's lane, otherwise both horizontal pieces overlap and look connected.
		before := map[string][]string{}
		for _, seg := range bending {
			if nodes[seg.from].col != col {
				continue
			}
			for _, other := range srcs {
				if other != seg.from && anchorY(other) == anchorY(seg.to) {
					if !contains(before[seg.from], other) {
						before[seg.from] = append(before[seg.from], other)
					}
				}
			}
		}
		var ordered []string
		state := map[string]bool{}
		var visit func(s string)
		visit = func(s string) {
			if state[s] {
				return // done, or a cycle we cannot satisfy anyway
			}
			state[s] = true
			for _, b := range before[s] {
				visit(b)
			}
			ordered = append(ordered, s)
		}
		for _, s := range srcs {
			visit(s)
		}
		gapSources[col] = ordered
	}
	gapW := make([]int, len(gapSources))
	for c, srcs := range gapSources {
		if c != maxLevel {
			gapW[c] = max(6, len(srcs)*2+4)
		}
	}

	colX := make([]int, len(layers))
	x := 1
	for c := 0; c <= maxLevel; c++ {
		colX[c] = x
		x += colW[c] + gapW[c]
	}
	width := x + 1
	height := maxSlots * rowH

	l := &Layout{
		Width: width, Height: height,
		masks:     make([]uint8, width*height),
		cellEdges: cellSets{}, arrows: cellSets{}, ports: cellSets{},
	}
	idx := func(cx, cy int) int { return cy*width + cx }
	mark := func(cx, cy int, m uint8, edge string) {
		if cx < 0 || cy < 0 || cx >= width || cy >= height {
			return
		}
		i := idx(cx, cy)
		l.masks[i] |= m
		l.cellEdges.add(i, edge)
	}
	hline := func(x0, x1, cy int, edge string) {
		a, b := x0, x1
		if a > b {
			a, b = b, a
		}
		for cx := a; cx < b; cx++ {
			mark(cx, cy, dirE, edge)
			mark(cx+1, cy, dirW, edge)
		}
		if a == b {
			mark(a, cy, 0, edge)
		}
	}
	vline := func(cx, y0, y1 int, edge string) {
		a, b := y0, y1
		if a > b {
			a, b = b, a
		}
		for cy := a; cy < b; cy++ {
			mark(cx, cy, dirS, edge)
			mark(cx, cy+1, dirN, edge)
		}
	}

	for _, s := range segments {
		from, to := nodes[s.from], nodes[s.to]
		sx := colX[from.col] + colW[from.col]
		tx := colX[to.col] - 1
		sy, ty := anchorY(s.from), anchorY(s.to)
		// stub coming out of the box border
		mark(sx, sy, dirW, s.edge)
		if from.real {
			l.ports.add(idx(sx-1, sy), s.edge)
		}
		if sy == ty {
			hline(sx, tx, sy, s.edge)
		} else {
			lane := indexOf(gapSources[from.col], s.from)
			lx := sx + 2 + lane*2
			hline(sx, lx, sy, s.edge)
			vline(lx, sy, ty, s.edge)
			hline(lx, tx, ty, s.edge)
		}
		if to.real {
			l.arrows.add(idx(tx, ty), s.edge)
		} else {
			// dummy: straight line across its column
			hline(tx, colX[to.col]+colW[to.col], ty, s.edge)
		}
	}

	// boxes in the order the nodes were created (declaration order)
	for _, name := range order {
		n := nodes[name]
		l.Boxes = append(l.Boxes, NodeBox{Name: n.id, X: colX[n.col], Y: slotY(n), W: colW[n.col], H: BoxH, Col: n.col})
	}
	return l
}

func contains(list []string, s string) bool { return indexOf(list, s) >= 0 }

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}
