package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// taskReadiness says whether the services a task depends on are up: running the task starts the ones that are not.
type taskReadiness struct {
	// missing are the dependencies that are not ready yet
	missing []string
	// failed are the missing ones that crashed or could not start
	failed []string
}

func (m *Model) taskReadiness(t *config.TaskConfig) taskReadiness {
	var r taskReadiness
	for _, d := range t.DependsOn {
		if m.sup.IsReady(d) {
			continue
		}
		r.missing = append(r.missing, d)
		if st := m.sup.State(d).Status; st == supervisor.Crashed || st == supervisor.Failed {
			r.failed = append(r.failed, d)
		}
	}
	return r
}

func (m *Model) openTasks() tea.Cmd {
	if len(m.sup.Config().TaskOrder) == 0 {
		return m.notify("no tasks: add a `tasks:` section to orbit.yaml", theme.Active().Muted)
	}
	m.taskIdx = 0
	m.mode = modeTasks
	return nil
}

func (m *Model) keyInTasks(k tea.KeyMsg) tea.Cmd {
	order := m.sup.Config().TaskOrder
	switch k.String() {
	case "esc", "q", "!":
		m.mode = modeNormal
	case "up", "k", "ctrl+p":
		m.taskIdx = max(0, m.taskIdx-1)
	case "down", "j", "ctrl+n":
		m.taskIdx = min(len(order)-1, m.taskIdx+1)
	case "g", "home":
		m.taskIdx = 0
	case "G", "end":
		m.taskIdx = len(order) - 1
	case "enter":
		m.mode = modeNormal
		if m.taskIdx >= 0 && m.taskIdx < len(order) {
			return m.runTask(order[m.taskIdx])
		}
	}
	return nil
}

func (m *Model) renderTasks(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	cfg := m.sup.Config()
	order := cfg.TaskOrder
	w := min(80, W-4)
	m.taskIdx = max(0, min(m.taskIdx, len(order)-1))
	maxRows := max(1, min(14, H-8))
	start := windowAround(m.taskIdx, maxRows, len(order))
	end := min(len(order), start+maxRows)

	nameW := 0
	for _, n := range order {
		nameW = max(nameW, len([]rune(n)))
	}
	nameW = min(nameW, 20)
	var content []string
	for i := start; i < end; i++ {
		t := cfg.Tasks[order[i]]
		sel := i == m.taskIdx
		bg := p.PanelAlt
		if sel {
			bg = p.Selection
		}
		r := m.taskReadiness(t)
		dot, dotColor := "●", p.Green
		hint, hintColor := "", p.Dim
		switch {
		case len(r.failed) > 0:
			dot, dotColor = "✖", p.Red
			hint, hintColor = strings.Join(r.failed, ", ")+" failed", p.Red
		case len(r.missing) > 0:
			dot, dotColor = "○", p.Orange
			hint, hintColor = "starts "+strings.Join(r.missing, ", "), p.Orange
		case len(t.DependsOn) == 0:
			dot, dotColor = "●", p.Muted
		}
		what := t.Description
		if what == "" {
			what = t.Cmd
		}
		hintW := min(len([]rune(hint)), 28)
		descW := max(1, w-4-2-2-nameW-2-hintW-2)
		l := newLine(bg)
		if sel {
			l.add(p.Accent, "▸ ").add(dotColor, dot+" ").bold(p.Text, theme.Fit(t.Name, nameW))
		} else {
			l.add(p.Dim, "  ").add(dotColor, dot+" ").add(p.Muted, theme.Fit(t.Name, nameW))
		}
		l.add(p.Dim, "  "+theme.Fit(what, descW)).add(hintColor, "  "+theme.Fit(hint, hintW))
		content = append(content, l.pad(w-4).String())
	}
	content = append(content,
		newLine(p.PanelAlt).add(p.Green, "●").add(p.Dim, " its services are up   ").add(p.Muted, "●").add(p.Dim, " needs no services").String(),
		newLine(p.PanelAlt).add(p.Orange, "○").add(p.Dim, " running it starts its services   ").add(p.Red, "✖").add(p.Dim, " one of them failed").String(),
		newLine(p.PanelAlt).add(p.Dim, "↑↓ select · enter run · esc close").String())
	h := end - start + 5
	x, y := center(W, H, w, h)
	return modalBox("Tasks", w, h, content), w, x, y
}
