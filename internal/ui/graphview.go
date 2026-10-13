package ui

import (
	"fmt"
	"strings"

	"github.com/JGoterris/orbit/internal/core/metrics"
	"github.com/JGoterris/orbit/internal/graph"
	"github.com/JGoterris/orbit/internal/ui/graphlayout"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// graphLayout is the layout of the current config's dependency graph, cached until the graph changes.
func (m *Model) graphLayout() *graphlayout.Layout {
	deps := m.sup.Deps()
	key := len(deps.Names)
	for _, n := range deps.Names {
		key = key*31 + len(n) + len(deps.Deps[n])*7
		for _, d := range deps.Deps[n] {
			key = key*31 + len(d)
		}
	}
	if m.layout == nil || m.layoutKey != key {
		m.layout = graphlayout.LayoutGraph(deps, deps.Names)
		m.layoutKey = key
	}
	return m.layout
}

// neighbourInDirection is the nearest box in a direction, for arrow-key navigation inside the graph.
func neighbourInDirection(l *graphlayout.Layout, from, dir string) string {
	var cur *graphlayout.NodeBox
	for i := range l.Boxes {
		if l.Boxes[i].Name == from {
			cur = &l.Boxes[i]
		}
	}
	if cur == nil {
		return ""
	}
	cx := func(b graphlayout.NodeBox) float64 { return float64(b.X) + float64(b.W)/2 }
	cy := func(b graphlayout.NodeBox) float64 { return float64(b.Y) + float64(b.H)/2 }
	best, bestScore := "", 0.0
	for _, b := range l.Boxes {
		if b.Name == from {
			continue
		}
		dx, dy := cx(b)-cx(*cur), cy(b)-cy(*cur)
		var primary, secondary float64
		switch dir {
		case "left":
			primary, secondary = -dx, abs(dy)
		case "right":
			primary, secondary = dx, abs(dy)
		case "up":
			primary, secondary = -dy, abs(dx)
		default:
			primary, secondary = dy, abs(dx)
		}
		if primary <= 0 {
			continue
		}
		score := primary + secondary*2.5
		if best == "" || score < bestScore {
			best, bestScore = b.Name, score
		}
	}
	return best
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// renderGraph is the dependency graph panel.
func (m *Model) renderGraph(w, h int, focused bool) []string {
	p := theme.Active()
	layout := m.graphLayout()
	deps := m.sup.Deps()
	upstream := graph.TransitiveDeps(deps, m.selected)
	downstream := graph.TransitiveDependents(deps, m.selected)

	grid := graphlayout.Paint(layout, graphlayout.PaintOptions{
		Text: p.Text, DefaultEdge: p.Edge,
		EdgeColor: func(edge string) string {
			dep, dependent, _ := strings.Cut(edge, ">")
			if upstream[dep] && (dependent == m.selected || upstream[dependent]) {
				return p.Upstream
			}
			if downstream[dependent] && (dep == m.selected || downstream[dep]) {
				return p.Downstream
			}
			return ""
		},
		Node: func(name string) graphlayout.NodeStyle {
			st := m.sup.State(name)
			svc := m.sup.Service(name)
			oneshot := svc != nil && svc.Oneshot
			style := theme.StyleFor(string(st.Status), oneshot)
			sub := style.Label
			if svc != nil && svc.Port != 0 {
				sub += fmt.Sprintf(" :%d", svc.Port)
			}
			if len(st.Mem) > 0 && m.sup.IsUp(name) {
				sub += " " + metrics.FormatBytes(st.Mem[len(st.Mem)-1])
			}
			border := p.Border
			switch {
			case name == m.selected:
				border = p.Accent
			case upstream[name]:
				border = p.Upstream
			case downstream[name]:
				border = p.Downstream
			}
			return graphlayout.NodeStyle{
				Icon: theme.StatusIcon(string(st.Status), m.tick, oneshot), IconColor: style.Color,
				Title: name, Subtitle: sub, SubtitleColor: style.Color, Border: border, Selected: name == m.selected,
			}
		},
	})

	viewW, viewH := max(1, w-2), max(1, h-3)
	var box *graphlayout.NodeBox
	for i := range layout.Boxes {
		if layout.Boxes[i].Name == m.selected {
			box = &layout.Boxes[i]
		}
	}
	m.geo.mu.Lock()
	ox, oy := m.geo.graphOffX, m.geo.graphOffY
	if layout.Width <= viewW {
		ox = 0
	} else if box != nil {
		if box.X-2 < ox {
			ox = max(0, box.X-2)
		}
		if box.X+box.W+2 > ox+viewW {
			ox = min(layout.Width-viewW, box.X+box.W+2-viewW)
		}
	}
	if layout.Height <= viewH {
		oy = 0
	} else if box != nil {
		if box.Y-1 < oy {
			oy = max(0, box.Y-1)
		}
		if box.Y+box.H+1 > oy+viewH {
			oy = min(layout.Height-viewH, box.Y+box.H+1-viewH)
		}
	}
	// never scroll past the edges
	ox = max(0, min(ox, layout.Width-viewW))
	oy = max(0, min(oy, layout.Height-viewH))
	padX := max(0, (viewW-layout.Width)/2)
	padY := max(0, (viewH-layout.Height)/2)
	m.geo.graphOffX, m.geo.graphOffY = ox, oy
	m.geo.graphPadX, m.geo.graphPadY = padX, padY
	m.geo.graphW, m.geo.graphH = w, h
	m.geo.mu.Unlock()

	var content []string
	for i := 0; i < padY; i++ {
		content = append(content, blank(w-2, p.Panel))
	}
	for y := oy; y < min(len(grid), oy+viewH-padY); y++ {
		l := newLine(p.Panel)
		l.add(p.Text, strings.Repeat(" ", padX))
		row := grid[y]
		from, to := ox, min(len(row), ox+viewW-padX)
		var run strings.Builder
		runFg, runBold := "", false
		flush := func() {
			if run.Len() > 0 {
				l.addStyled(runFg, "", runBold, run.String())
				run.Reset()
			}
		}
		for x := from; x < to; x++ {
			c := row[x]
			if c.Fg != runFg || c.Bold != runBold {
				flush()
				runFg, runBold = c.Fg, c.Bold
			}
			run.WriteString(c.Ch)
		}
		flush()
		content = append(content, l.String())
	}
	// pad to leave the legend on the last row
	for len(content) < h-3 {
		content = append(content, blank(w-2, p.Panel))
	}
	legend := newLine(p.Panel).add(p.Dim, " ──▶ starts before   ").
		add(p.Upstream, fmt.Sprintf("■ needs (%d)   ", len(upstream))).
		add(p.Downstream, fmt.Sprintf("■ used by (%d)   ", len(downstream))).
		add(p.Dim, "←↑↓→ move · space start/stop · enter logs")
	content = append(content, legend.String())

	up := 0
	for _, n := range m.names() {
		if m.sup.IsUp(n) {
			up++
		}
	}
	return panel(panelOpts{title: fmt.Sprintf("Dependency graph · %d/%d up", up, len(m.names())), focused: focused}, w, h, content)
}
