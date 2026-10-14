package ui

import (
	"fmt"
	"strings"

	"github.com/JGoterris/orbit/internal/core/metrics"
	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

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
	nameW := max(6, w-func() int {
		if compact {
			return 14
		}
		return 35
	}())

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
			l.bold(p.Text, theme.Fit(name, nameW-2))
		} else {
			l.add(p.Text, theme.Fit(name, nameW-2))
		}
		switch st.Watch {
		case "pending":
			l.add(p.Yellow, " ↻")
		case "active", "paused":
			l.add(p.Accent, " ↻")
		default:
			l.add(p.Dim, "  ")
		}
		if !compact {
			l.add(badge.Color, " "+badge.Label)
		}
		port := ""
		if svc.Port != 0 {
			port = fmt.Sprintf(" :%d", svc.Port)
		}
		l.add(p.Muted, theme.Fit(port, 7))
		if !compact {
			l.add(p.Muted, padStart(cpu, 4))
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
