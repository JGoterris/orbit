package ui

import (
	"fmt"
	"strings"

	"github.com/JGoterris/orbit/internal/core/metrics"
	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// listColumns says how the Services panel spends its width. The name asks for what it needs, up to a share of the
// panel, so a very long one cannot push the other columns out for good; the columns then come in priority order
// (port, uptime, memory, cpu, type) while they fit, and whatever is left goes back to the name.
type listCols struct {
	name                          int
	port, uptime, mem, cpu, badge bool
}

const (
	colPortW   = 7
	colUptimeW = 7
	colMemW    = 7 // memory and the leak arrow
	colCPUW    = 4
	colBadgeW  = 5
)

func (m *Model) listColumns(names []string, w int, compact bool) listCols {
	avail := w - 2 - 5 // the border, then the selection bar, the status icon and the watch mark
	longest := 0
	for _, n := range names {
		longest = max(longest, len([]rune(n)))
	}
	want := min(longest, max(12, (w-2)*45/100))
	want = max(6, min(want, avail))
	rest := avail - want
	var c listCols
	take := func(on *bool, width int) bool {
		if rest < width {
			return false
		}
		*on = true
		rest -= width
		return true
	}
	// compact lists (the narrow sidebar of the graph and logs views) only ever show the port
	if take(&c.port, colPortW) && !compact {
		_ = take(&c.uptime, colUptimeW) && take(&c.mem, colMemW) && take(&c.cpu, colCPUW) && take(&c.badge, colBadgeW)
	}
	c.name = max(6, avail-(avail-want-rest))
	return c
}

func padStart(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	return strings.Repeat(" ", w-n) + s
}

// renderServiceList is the Services panel: one row per service, with the selection bar.
func (m *Model) renderServiceList(w, h int, focused, compact bool) []string {
	p := theme.Active()
	names := m.names()
	up := 0
	for _, n := range names {
		if m.sup.IsUp(n) {
			up++
		}
	}
	cols := m.listColumns(names, w, compact)
	nameW := cols.name

	rows := max(1, h-2)
	selIdx := 0
	for i, n := range names {
		if n == m.selected {
			selIdx = i
		}
	}
	start := max(0, min(selIdx-rows/2, len(names)-rows))
	inner := w - 2

	m.geo.mu.Lock()
	m.geo.listFirst = start
	m.geo.mu.Unlock()

	var content []string
	for i := start; i < len(names) && i < start+rows; i++ {
		name := names[i]
		st := m.sup.State(name)
		svc := m.sup.Service(name)
		if svc == nil {
			continue
		}
		style := theme.StyleFor(string(st.Status), svc.Oneshot)
		isSel := name == m.selected
		bg := p.Panel
		if isSel {
			bg = p.Selection
		}
		upNow := st.StartedAt != 0 && (st.Status == supervisor.Healthy || st.Status == supervisor.Running ||
			st.Status == supervisor.Unhealthy || st.Status == supervisor.Starting)
		cpu, mem := "", ""
		if len(st.CPU) > 0 && upNow {
			cpu = fmt.Sprintf("%d%%", int(st.CPU[len(st.CPU)-1]+0.5))
		}
		if len(st.Mem) > 0 && upNow {
			mem = metrics.FormatBytes(st.Mem[len(st.Mem)-1])
		}
		uptime := style.Label
		switch {
		case upNow:
			uptime = metrics.FormatDuration(float64(nowMs() - st.StartedAt))
		case st.Status == supervisor.Stopped:
			uptime = ""
		}
		badge := theme.TypeBadge(string(svc.Type))

		l := newLine(bg)
		if isSel {
			l.add(p.Accent, "▌")
		} else {
			l.add(p.Panel, " ")
		}
		l.add(style.Color, theme.StatusIcon(string(st.Status), m.tick, svc.Oneshot)+" ")
		if isSel {
			l.bold(p.Text, theme.FitMid(name, nameW))
		} else {
			l.add(p.Text, theme.FitMid(name, nameW))
		}
		switch st.Watch {
		case "pending":
			l.add(p.Yellow, " ↻")
		case "active", "paused":
			l.add(p.Accent, " ↻")
		default:
			l.add(p.Dim, "  ")
		}
		if cols.badge {
			l.add(badge.Color, " "+badge.Label)
		}
		port := ""
		if svc.Port != 0 {
			port = fmt.Sprintf(" :%d", svc.Port)
		}
		if cols.port {
			l.add(p.Muted, theme.Fit(port, 7))
		}
		if cols.cpu {
			l.add(p.Muted, padStart(cpu, 4))
		}
		if cols.mem {
			memColor := p.Dim
			if r := st.Resources; r != nil {
				switch r.Level {
				case resources.LevelOver:
					memColor = p.Red
				case resources.LevelWarn:
					memColor = p.Orange
				}
			}
			l.add(memColor, padStart(mem, 6))
			if st.Resources != nil && st.Resources.Leak != nil {
				l.add(p.Orange, "↗")
			} else {
				l.add(p.Text, " ")
			}
		}
		if cols.uptime {
			upColor := p.Dim
			if st.Status == supervisor.Crashed || st.Status == supervisor.Failed {
				upColor = p.Red
			}
			l.add(upColor, theme.Fit(uptime, 7))
		}
		content = append(content, l.pad(inner).String())
	}
	return panel(panelOpts{title: fmt.Sprintf("Services %d/%d", up, len(names)), focused: focused}, w, h, content)
}
