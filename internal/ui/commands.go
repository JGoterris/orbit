package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// commands is what the command palette offers, rebuilt each time it is needed.
func (m *Model) commands() []command {
	p := theme.Active()
	sup := m.sup
	names := m.names()
	var list []command
	add := func(id, label, hint string, run func(m *Model) tea.Cmd) {
		list = append(list, command{id: id, label: label, hint: hint, run: run})
	}

	add("start-all", "Start all services", "S", func(m *Model) tea.Cmd { return m.op(sup.StartAll) })
	add("stop-all", "Stop all services", "X", func(m *Model) tea.Cmd { return m.op(sup.StopAll) })
	add("restart-all", "Restart all running services", "R", func(m *Model) tea.Cmd { return m.restartAll() })
	for i, v := range views {
		add("view-"+v.id, "View: "+v.label, fmt.Sprint(i+1), func(m *Model) tea.Cmd { m.setView(v.id); return nil })
	}
	add("grow-panel", "Grow focused panel", "+", func(m *Model) tea.Cmd { m.resize(1); return nil })
	add("shrink-panel", "Shrink focused panel", "-", func(m *Model) tea.Cmd { m.resize(-1); return nil })
	add("reset-sizes", "Reset panel sizes", "=", func(m *Model) tea.Cmd {
		m.sidebarDelta = map[string]int{}
		m.detailH = 0
		return nil
	})
	add("usage-range", "Cycle the cpu / memory chart range (2m · 15m · 1h)", "h", func(m *Model) tea.Cmd { return m.cycleRange() })
	add("toggle-zoom", "Toggle zoom of the focused panel", "z", func(m *Model) tea.Cmd { m.zoomed = !m.zoomed; return nil })

	cfg := sup.Config()
	for _, g := range cfg.GroupOrder {
		members := cfg.Groups[g]
		hint := strings.Join(members, ",")
		add("group-start-"+g, "Start group "+g, hint, func(m *Model) tea.Cmd { return m.op(func() { sup.StartMany(members) }) })
		add("group-stop-"+g, "Stop group "+g, hint, func(m *Model) tea.Cmd {
			return m.op(func() {
				for _, mm := range members {
					sup.Stop(mm, supervisor.StopOptions{})
				}
			})
		})
	}
	for _, n := range names {
		n := n
		svc := sup.Service(n)
		if svc == nil {
			continue
		}
		hint := ""
		if len(svc.DependsOn) > 0 {
			hint = "+ " + strings.Join(svc.DependsOn, ",")
		}
		add("start-"+n, "Start "+n, hint, func(m *Model) tea.Cmd { return m.op(func() { sup.Start(n) }) })
		add("stop-"+n, "Stop "+n, "", func(m *Model) tea.Cmd { return m.op(func() { sup.Stop(n, supervisor.StopOptions{}) }) })
		add("restart-"+n, "Restart "+n, "", func(m *Model) tea.Cmd { return m.op(func() { sup.Restart(n) }) })
		add("console-"+n, "Console in "+n, "i", func(m *Model) tea.Cmd { return m.openConsole(n) })
		if svc.Watch != nil {
			add("watch-"+n, "Pause/resume watching "+n, "W", func(m *Model) tea.Cmd { return m.toggleWatch(n) })
		}
		add("logs-"+n, "Logs of "+n, "", func(m *Model) tea.Cmd {
			m.selected = n
			m.logScope = "selected"
			m.setView("logs")
			m.focus = paneLogs
			return nil
		})
		if svc.Port != 0 || svc.URL != "" {
			h := svc.URL
			if h == "" {
				h = fmt.Sprintf(":%d", svc.Port)
			}
			add("open-"+n, "Open "+n+" in browser", h, func(m *Model) tea.Cmd { return m.openService(n) })
		}
	}
	add("clear-logs", "Clear all logs", "", func(m *Model) tea.Cmd { sup.ClearLogs(""); return nil })
	add("toggle-fold", "Toggle stack trace folding", "b", func(m *Model) tea.Cmd { m.fold = !m.fold; return nil })
	add("toggle-wrap", "Toggle log line wrap", "w", func(m *Model) tea.Cmd { m.wrap = !m.wrap; return nil })
	add("export-logs", "Export visible logs to a file", "E", func(m *Model) tea.Cmd { return m.exportLogs() })
	add("copy-logs", "Copy visible logs to the clipboard", "Y", func(m *Model) tea.Cmd { return m.copyLines(m.rawViewLines(), "all logs in view") })
	add("reload", "Apply orbit.yaml changes", "U", func(m *Model) tea.Cmd { return m.openReload() })
	add("theme", "Change theme…", "T", func(m *Model) tea.Cmd { m.openThemePicker(); return nil })
	add("toggle-notifications", "Toggle desktop notifications (service down)", "", func(m *Model) tea.Cmd {
		if m.opts.SetNotifications == nil || m.opts.Notifications == nil {
			return m.notify("notifications are not configurable here", p.Yellow)
		}
		on := !m.opts.Notifications()
		if m.opts.SetNotifications(on) {
			state, color := "off", p.Muted
			if on {
				state, color = "on", p.Green
			}
			return m.notify("desktop notifications "+state, color)
		}
		return m.notify("could not save the setting to the config file", p.Yellow)
	})
	add("toggle-time", "Toggle log timestamps", "t", func(m *Model) tea.Cmd { m.showTime = !m.showTime; return nil })
	add("env", "Show environment variables", "e", func(m *Model) tea.Cmd { m.openEnv(); return nil })
	add("help", "Show keyboard shortcuts", "?", func(m *Model) tea.Cmd { m.mode = modeHelp; return nil })
	add("quit", "Quit orbit", "q", func(m *Model) tea.Cmd { return m.requestQuit() })
	add("quit-detach", "Quit orbit and leave services running", "q d", func(m *Model) tea.Cmd {
		if sup.OwnedRunningCount() > 0 {
			return m.doQuit("detach")
		}
		return m.doQuit("stop")
	})
	return list
}

func (m *Model) restartAll() tea.Cmd {
	sup := m.sup
	return m.op(func() {
		for _, n := range m.names() {
			if sup.IsUp(n) {
				sup.Restart(n)
			}
		}
	})
}

func (m *Model) cycleRange() tea.Cmd {
	for i, r := range resources.Ranges {
		if r == m.resRange {
			m.resRange = resources.Ranges[(i+1)%len(resources.Ranges)]
			break
		}
	}
	m.historyFor = ""
	return m.loadHistory()
}

func (m *Model) openEnv() {
	if m.selected == "" {
		return
	}
	m.envScroll, m.envReveal = 0, false
	m.mode = modeEnv
}

// resizeTarget: which divider the focused panel controls, and whether growing the panel grows or shrinks it.
func (m *Model) resizeTarget() (divider string, sign int, ok bool) {
	switch {
	case m.focus == paneServices:
		return "sidebar", 1, true
	case m.focus == paneGraph || (m.focus == paneLogs && m.view == "logs"):
		return "sidebar", -1, true
	case m.focus == paneDetail:
		return "detail", 1, true
	case m.focus == paneLogs && m.view == "dashboard":
		return "detail", -1, true
	}
	return "", 0, false
}

func (m *Model) resize(dir int) {
	divider, sign, ok := m.resizeTarget()
	if !ok || m.zoomed {
		return
	}
	if divider == "sidebar" {
		m.sidebarDelta[m.view] = m.clampSidebar(m.sidebarW()+2*dir*sign) - m.defaultSidebarW()
	} else {
		m.detailH = m.clampDetail(m.detailRows() + dir*sign)
	}
}

func (m *Model) resetSize() {
	divider, _, ok := m.resizeTarget()
	if !ok || m.zoomed {
		return
	}
	if divider == "sidebar" {
		m.sidebarDelta[m.view] = 0
	} else {
		m.detailH = 0
	}
}
