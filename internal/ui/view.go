package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/ui/theme"
)

// View draws the whole screen.
func (m *Model) View() string {
	if m.splash != nil {
		if s, ok := m.renderSplash(); ok {
			return s
		}
	}
	return m.viewApp()
}

// viewApp draws the app itself.
func (m *Model) viewApp() string {
	W, H := m.width, m.height
	if W < 20 || H < 6 {
		return "orbit needs a bigger terminal"
	}
	p := theme.Active()
	m.geo.mu.Lock()
	m.geo.hits = nil
	m.geo.mu.Unlock()

	rows := make([]string, 0, H)
	rows = append(rows, clamp(m.renderHeader(W), W, p.PanelAlt))

	pluginFilter := m.plug.filtering != nil
	bodyH := H - 2
	if m.mode == modeFilter || pluginFilter {
		bodyH--
	}
	rows = append(rows, m.renderBody(W, bodyH)...)
	switch {
	case m.mode == modeFilter:
		rows = append(rows, clamp(m.renderFilterBar(W), W, p.PanelAlt))
	case pluginFilter:
		rows = append(rows, clamp(m.renderPluginFilterBar(W), W, p.PanelAlt))
	}
	rows = append(rows, clamp(m.renderFooter(W), W, p.PanelAlt))
	for len(rows) < H {
		rows = append(rows, blank(W, p.Bg))
	}
	rows = rows[:H]

	var modal []string
	var mw, mx, my int
	if m.view == "git" && m.mode == modeNormal {
		if gm, gw, gx, gy, ok := m.renderGitModal(W, H); ok {
			modal, mw, mx, my = gm, gw, gx, gy
		}
	}
	switch m.mode {
	case modeTheme:
		modal, mw, mx, my = m.renderThemePicker(W, H)
	case modeTasks:
		modal, mw, mx, my = m.renderTasks(W, H)
	case modeProfiles:
		modal, mw, mx, my = m.renderProfiles(W, H)
	case modePalette:
		modal, mw, mx, my = m.renderPalette(W, H)
	case modeHelp:
		modal, mw, mx, my = m.renderHelp(W, H)
	case modeEnv:
		if m.selected != "" {
			modal, mw, mx, my = m.renderEnv(W, H)
		}
	case modeReload:
		if m.reload != nil {
			modal, mw, mx, my = m.renderReload(W, H)
		}
	case modePlugin:
		modal, mw, mx, my = m.renderPluginDialog(W, H)
	case modeProjects:
		modal, mw, mx, my = m.renderProjects(W, H)
	case modeQuit, modeStopping, modeSwitch:
		modal, mw, mx, my = m.renderConfirm(W, H)
	case modeConsole:
		if m.console != nil {
			modal, mw, mx, my = m.renderConsole(W, H)
		}
	}
	if modal != nil {
		rows = overlay(rows, modal, mw, mx, my, W)
	}
	return strings.Join(rows, "\n")
}

func (m *Model) renderHeader(W int) string {
	p := theme.Active()
	l := newLine(p.PanelAlt)
	l.add("", " ").bold(p.Accent, "◉ orbit").add(p.Dim, " · ").add(p.Text, m.sup.Config().Name)
	if prof := m.sup.Config().Profiles; len(prof) > 0 {
		l.add(p.Dim, " ⚑ ").add(p.Accent, strings.Join(prof, ","))
	}
	l.add(p.Dim, "    ")
	var tabs []tabHit
	for i, v := range m.allViews() {
		label := fmt.Sprintf(" %d %s ", i+1, v.label)
		x0 := l.width()
		if v.id == m.view {
			l.addStyled(p.Bg, p.Accent, true, label)
		} else {
			l.add(p.Muted, label)
		}
		tabs = append(tabs, tabHit{x0: x0, x1: l.width(), view: v.id})
	}
	m.geo.mu.Lock()
	m.geo.tabs = tabs
	m.geo.mu.Unlock()
	if m.zoomed {
		l.add(p.Accent, "  ⛶ zoom")
	}
	if m.plug.running != "" {
		l.add(p.Yellow, "  "+theme.StatusIcon("starting", m.tick, false)+" "+m.plug.running)
	}
	if pend := m.sup.Pending(); pend != nil {
		if pend.Error != "" {
			l.add(p.Red, "  ✖ orbit.yaml invalid")
		} else {
			l.add(p.Orange, "  ⟳ orbit.yaml changed · U")
		}
	}
	up, bad, busy := m.counts()
	total := len(m.names())
	right := newLine(p.PanelAlt)
	right.add(p.Green, fmt.Sprintf("● %d up", up))
	if busy > 0 {
		right.add(p.Yellow, fmt.Sprintf("  ◐ %d", busy))
	}
	if bad > 0 {
		right.add(p.Red, fmt.Sprintf("  ✖ %d", bad))
	}
	right.add(p.Dim, fmt.Sprintf("  ○ %d ", total-up-bad-busy))
	badges, badgeHits := m.headerPluginLine(p.PanelAlt)
	gap := max(1, W-l.width()-right.width()-badges.width())
	l.add("", strings.Repeat(" ", gap))
	x0 := l.width()
	l.raw(badges.String(), badges.width())
	l.raw(right.String(), right.width())
	for i := range badgeHits {
		badgeHits[i].x0 += x0
		badgeHits[i].x1 += x0
	}
	m.geo.mu.Lock()
	m.geo.badges = badgeHits
	m.geo.mu.Unlock()
	return l.String()
}

func (m *Model) renderBody(W, H int) []string {
	vd := m.viewDef()
	sidebarW := m.sidebarW()
	showList := vd.has(paneServices) && (!m.zoomed || m.focus == paneServices)
	showRight := !m.zoomed || m.focus != paneServices
	compact := (vd.compactSidebar || sidebarW < 36) && !m.zoomed

	listW := 0
	var list []string
	if showList {
		listW = sidebarW
		if m.zoomed {
			listW = W
		}
		list = m.renderServiceList(listW, H, m.focus == paneServices, compact)
	}
	m.geo.mu.Lock()
	m.geo.listX, m.geo.listY, m.geo.listW, m.geo.listH = 0, 1, listW, H
	m.geo.logH, m.geo.graphW = 0, 0
	m.geo.mu.Unlock()
	if !showRight {
		return list
	}
	rightW := W - listW
	var right []string
	switch {
	case vd.plugin != nil:
		right = m.renderPluginView(vd, rightW, H, listW)
	case m.view == "graph":
		right = m.renderGraph(rightW, H, m.focus == paneGraph)
		m.geo.mu.Lock()
		m.geo.graphX, m.geo.graphY = listW, 1
		m.geo.mu.Unlock()
	case m.view == "logs":
		title := "Logs · all services  (a: only selected)"
		if m.logScope != "all" {
			title = "Logs · " + m.selected + "  (a: all)"
		}
		right = m.renderLogView(title, rightW, H, m.focus == paneLogs)
		m.geo.mu.Lock()
		m.geo.logX, m.geo.logY = listW, 1
		m.geo.mu.Unlock()
	case m.view == "git":
		right = m.renderGit(rightW, H)
	default:
		right = m.renderDashboard(rightW, H, listW)
	}
	if !showList {
		return right
	}
	return hconcat(list, right)
}

func (m *Model) renderDashboard(w, H, x int) []string {
	if m.selected == "" {
		p := theme.Active()
		msg := newLine(p.Panel).add(p.Muted, "This project has no services. Add an orbit.yaml or a docker-compose.yml.").String()
		return panel(panelOpts{}, w, H, []string{"", msg})
	}
	detailRows := m.detailRows() + m.pluginDetailExtra(m.selected, w-4)
	switch {
	case m.zoomed && m.focus == paneDetail:
		m.hit(paneDetail, x, 1, w, H, 0, 0)
		return m.renderDetail(m.selected, w, H, true, true, m.height-6)
	case m.zoomed && m.focus == paneLogs:
		m.geo.mu.Lock()
		m.geo.logX, m.geo.logY = x, 1
		m.geo.mu.Unlock()
		return m.renderLogView("Logs · "+m.selected, w, H, true)
	case m.zoomed:
		return fill(w, H, theme.Active().Bg)
	}
	detailH := min(detailRows, H-3)
	detail := m.renderDetail(m.selected, w, detailH, m.focus == paneDetail, false, detailH-2)
	m.hit(paneDetail, x, 1, w, detailH, 0, 0)
	m.geo.mu.Lock()
	m.geo.logX, m.geo.logY = x, 1+detailH
	m.geo.mu.Unlock()
	logsPanel := m.renderLogView("Logs · "+m.selected, w, H-detailH, m.focus == paneLogs)
	return append(detail, logsPanel...)
}

func (m *Model) renderFilterBar(W int) string {
	p := theme.Active()
	l := newLine(p.PanelAlt)
	label := "filter "
	placeholder := "hide lines not matching (regex) · tab: search instead · enter keep · esc clear"
	if m.barKind == "search" {
		label = "search "
		placeholder = "highlight matches (regex), n/N to jump · tab: filter instead · enter go · esc clear"
	}
	l.add("", " ").add(p.Accent, label)
	w := max(1, W-1-len(label))
	l.raw(m.bar.viewWithPlaceholder(w, placeholder, p.Text, p.Dim, p.PanelAlt, true), w)
	return l.String()
}

var defaultHints = [][2]string{
	{"space", "start/stop"}, {"r", "restart"}, {"S/X", "all"}, {"l", "logs"}, {"tab", "focus"}, {"z", "zoom"},
	{"/", "filter"}, {"i", "console"}, {"o", "open"}, {":", "commands"}, {"?", "help"}, {"q", "quit"},
}

// withHint puts hint before the one whose key is before.
func withHint(hints [][2]string, hint [2]string, before string) [][2]string {
	out := make([][2]string, 0, len(hints)+1)
	for _, h := range hints {
		if h[0] == before {
			out = append(out, hint)
		}
		out = append(out, h)
	}
	return out
}

var copyHints = [][2]string{
	{"j/k", "move"}, {"ctrl+d/u", "page"}, {"v", "start/clear selection"}, {"enter/space", "open/close trace"},
	{"y", "copy"}, {"esc", "cancel"},
}

// fitHints keeps the hints that fit in width columns, in order, so none is cut in half. When some must go, "esc",
// "?" and "q" stay (they move to the end): esc is the way out, "?" says where to find every other key.
func fitHints(hints [][2]string, width int) [][2]string {
	cost := func(h [2]string) int { return ansi.StringWidth(h[0]) + 1 + ansi.StringWidth(h[1]) + 3 }
	fixed := func(h [2]string) bool { return h[0] == "esc" || h[0] == "?" || h[0] == "q" }
	total := 0
	for _, h := range hints {
		total += cost(h)
	}
	if total <= width {
		return hints
	}
	var keep, tail [][2]string
	used := 0
	for _, h := range hints {
		if fixed(h) {
			tail = append(tail, h)
			used += cost(h)
		}
	}
	for _, h := range hints {
		if fixed(h) {
			continue
		}
		if used+cost(h) > width {
			break
		}
		used += cost(h)
		keep = append(keep, h)
	}
	return append(keep, tail...)
}

func (m *Model) renderPluginFilterBar(W int) string {
	p := theme.Active()
	l := newLine(p.PanelAlt)
	l.add("", " ").add(p.Accent, "filter ")
	w := max(1, W-8)
	l.raw(m.plug.filterBar.viewWithPlaceholder(w, "narrow the list · enter keep · esc clear", p.Text, p.Dim, p.PanelAlt, true), w)
	return l.String()
}

func (m *Model) renderFooter(W int) string {
	p := theme.Active()
	l := newLine(p.PanelAlt)
	l.add("", " ")
	if m.toast != nil {
		l.add(m.toast.color, m.toast.text)
	} else {
		hints := defaultHints
		if len(m.sup.Config().TaskOrder) > 0 {
			hints = withHint(hints, [2]string{"!", "tasks"}, ":")
		}
		if len(m.sup.Config().AvailableProfiles) > 0 && m.view != "git" {
			hints = withHint(hints, [2]string{"p", "profiles"}, ":")
		}
		switch {
		case m.mode == modeCopy:
			hints = copyHints
		case m.view == "git":
			hints = m.gitHints()
		case m.viewDef().plugin != nil:
			hints = m.pluginHints()
		}
		for _, h := range fitHints(hints, W-1) {
			l.add(p.Accent, h[0]).add(p.Dim, " "+h[1]+"   ")
		}
	}
	if m.selected != "" && m.viewDef().has(paneServices) {
		st := m.sup.State(m.selected)
		svc := m.sup.Service(m.selected)
		style := theme.StyleFor(string(st.Status), svc != nil && svc.Oneshot)
		right := newLine(p.PanelAlt).add(style.Color, style.Icon+" ").add(p.Muted, m.selected+" ")
		if gap := W - l.width() - right.width(); gap > 0 {
			l.add("", strings.Repeat(" ", gap))
			l.raw(right.String(), right.width())
		}
	}
	return ansi.Truncate(l.String(), W, "")
}
