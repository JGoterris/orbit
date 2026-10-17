package ui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/projects"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// projectRow is one line of the project picker.
type projectRow struct {
	key   string
	label string
	hint  string
	// path is the directory this row opens
	path string
	// entry is the registry entry behind the row (nil for a typed path)
	entry *projects.Entry
	// missing: the typed path is not a folder, or a remembered project's folder is gone
	missing bool
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// projectRows are the rows of the picker: a typed path first ("open this folder"), then the registry (pinned,
// then recent) fuzzy-filtered by what was typed.
func projectRows(entries []projects.Entry, statuses map[string]projects.Status, query, currentRoot string) []projectRow {
	q := strings.TrimSpace(query)
	var rows []projectRow
	typed := projects.LooksLikePath(q)
	if typed {
		path := projects.ExpandPath(q, "")
		hint := ""
		if !isDir(path) {
			hint = "not a folder"
		}
		rows = append(rows, projectRow{key: "open:" + path, label: "Open folder " + path, hint: hint, path: path, missing: !isDir(path)})
	}
	type scored struct {
		e     projects.Entry
		score int
		i     int
	}
	var list []scored
	for i, e := range projects.Sort(entries) {
		var s int
		var ok bool
		if typed {
			s, ok = fuzzyScore(projects.ExpandPath(q, ""), e.Path)
		} else {
			s, ok = fuzzyScore(q, e.Name+" "+e.Path)
		}
		if ok {
			list = append(list, scored{e, s, i})
		}
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && (list[j].score < list[j-1].score || (list[j].score == list[j-1].score && list[j].i < list[j-1].i)); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	for _, s := range list {
		e := s.e
		st, hasStatus := statuses[e.Path]
		var flags []string
		if e.Path == currentRoot {
			flags = append(flags, "current")
		}
		if hasStatus && st.OpenIn != 0 {
			flags = append(flags, fmt.Sprintf("open in pid %d", st.OpenIn))
		}
		if hasStatus && st.Running > 0 {
			flags = append(flags, fmt.Sprintf("● %d up", st.Running))
		}
		if hasStatus && !st.Exists {
			flags = append(flags, "missing")
		}
		label := e.Name + "  " + e.Path
		if e.Pinned {
			label = "★ " + label
		}
		entry := e
		rows = append(rows, projectRow{
			key: e.Path, label: label, hint: strings.Join(flags, " · "), path: e.Path, entry: &entry,
			missing: hasStatus && !st.Exists,
		})
	}
	return rows
}

func (m *Model) openProjects() {
	m.projEntries = projects.Read()
	m.projStatuses = map[string]projects.Status{}
	for _, e := range m.projEntries {
		m.projStatuses[e.Path] = projects.StatusOf(e)
	}
	m.projQuery = input{}
	m.projIdx = 0
	m.mode = modeProjects
}

func (m *Model) projRows() []projectRow {
	return projectRows(m.projEntries, m.projStatuses, m.projQuery.value(), m.sup.Config().Root)
}

func (m *Model) renderProjects(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	rows := m.projRows()
	w := min(90, W-4)
	maxRows := max(1, min(14, H-8))
	start := windowAround(m.projIdx, maxRows, len(rows))
	end := min(len(rows), start+maxRows)
	var content []string
	content = append(content, newLine(p.PanelAlt).add(p.Accent, "› ").
		raw(m.projQuery.viewWithPlaceholder(w-6, "search projects, or type a path (/, ~, ./) and press tab to complete", p.Text, p.Dim, p.PanelAlt, true), w-6).String())
	content = append(content, newLine(p.PanelAlt).add(p.Border, strings.Repeat("─", w-4)).String())
	if len(rows) == 0 {
		content = append(content, newLine(p.PanelAlt).add(p.Dim, "no projects yet: type a folder path to open one").String())
	}
	for i := start; i < end; i++ {
		r := rows[i]
		sel := i == m.projIdx
		bg := p.PanelAlt
		if sel {
			bg = p.Selection
		}
		l := newLine(bg)
		color := p.Muted
		switch {
		case r.missing:
			color = p.Red
		case sel:
			color = p.Text
		}
		if sel {
			l.add(p.Accent, "▸ ")
		} else {
			l.add(p.Dim, "  ")
		}
		l.add(color, theme.Fit(r.label, max(1, w-8-len([]rune(r.hint)))))
		hint := p.Dim
		if r.missing {
			hint = p.Red
		}
		l.add(hint, r.hint)
		content = append(content, l.pad(w-4).String())
	}
	h := (end - start) + 6
	for len(content) < h-3 {
		content = append(content, blank(w-4, p.PanelAlt))
	}
	content = append(content, newLine(p.PanelAlt).add(p.Dim, "enter open · tab complete path · ctrl+f pin · ctrl+x forget · esc close").String())
	x, y := center(W, H, w, h)
	return modalBox("Projects", w, h, content), w, x, y
}

func (m *Model) keyInProjects(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	p := theme.Active()
	rows := m.projRows()
	var row *projectRow
	if m.projIdx >= 0 && m.projIdx < len(rows) {
		row = &rows[m.projIdx]
	}
	switch s {
	case "esc":
		m.mode = modeNormal
	case "up", "ctrl+p":
		m.projIdx = max(0, m.projIdx-1)
	case "down", "ctrl+n":
		m.projIdx = min(max(0, len(rows)-1), m.projIdx+1)
	case "tab":
		if q := m.projQuery.value(); projects.LooksLikePath(q) {
			m.projQuery.set(projects.CompletePath(q, ""))
			m.projIdx = 0
		}
	case "ctrl+f":
		if row != nil && row.entry != nil {
			projects.SetPinned(row.entry.Path, !row.entry.Pinned)
			m.projEntries = projects.Read()
		}
	case "ctrl+x":
		if row != nil && row.entry != nil {
			projects.Forget(row.entry.Path)
			m.projEntries = projects.Read()
		}
	case "enter":
		if row == nil {
			return nil
		}
		if row.missing {
			return m.notify(row.path+" is not a folder", p.Red)
		}
		m.pendingDir = row.path
		if m.sup.OwnedRunningCount() == 0 && m.runningTasks() == 0 {
			return m.openProject(row.path, "stop")
		}
		m.mode = modeSwitch
	default:
		before := m.projQuery.value()
		m.projQuery.handle(k)
		if m.projQuery.value() != before {
			m.projIdx = 0
		}
	}
	return nil
}

func (m *Model) keyInSwitch(k tea.KeyMsg) tea.Cmd {
	if m.pendingDir == "" {
		m.mode = modeNormal
		return nil
	}
	switch k.String() {
	case "y", "Y", "s", "S", "enter":
		return m.openProject(m.pendingDir, "stop")
	case "d", "D":
		return m.openProject(m.pendingDir, "detach")
	case "n", "esc", "q":
		m.pendingDir = ""
		m.mode = modeNormal
	}
	return nil
}

// openProject asks the owner to switch; keys are ignored while the services are dealt with.
func (m *Model) openProject(dir, how string) tea.Cmd {
	if m.opts.OpenProject == nil {
		m.mode = modeNormal
		return m.notify("switching projects is not available here", theme.Active().Yellow)
	}
	m.mode = modeStopping
	m.busyHow = how
	open := m.opts.OpenProject
	return func() tea.Msg {
		sup, relaunch, err := open(dir, how)
		if err != nil {
			return projectSwitchFailedMsg{err}
		}
		return projectSwitchedMsg{sup: sup, relaunch: relaunch}
	}
}
