package ui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// profilesLoadedMsg: the configuration the chosen set of profiles gives, ready to be offered.
type profilesLoadedMsg struct {
	cfg *config.OrbitConfig
	err error
}

func (m *Model) openProfiles() tea.Cmd {
	cfg := m.sup.Config()
	if len(cfg.AvailableProfiles) == 0 {
		return m.notify("no profiles: add a `profiles:` section to orbit.yaml", theme.Active().Muted)
	}
	if m.opts.LoadProfiles == nil {
		return m.notify("profiles cannot be changed here", theme.Active().Yellow)
	}
	m.profIdx = 0
	m.profSel = map[string]bool{}
	for _, n := range cfg.Profiles {
		m.profSel[n] = true
	}
	m.mode = modeProfiles
	return nil
}

// chosenProfiles is the checked set: the ones already active keep their order (a later profile wins over an earlier
// one), the newly checked go after them in the order orbit.yaml declares them.
func (m *Model) chosenProfiles() []string {
	cfg := m.sup.Config()
	var out []string
	for _, n := range cfg.Profiles {
		if m.profSel[n] {
			out = append(out, n)
		}
	}
	for _, p := range cfg.AvailableProfiles {
		if m.profSel[p.Name] && !slices.Contains(out, p.Name) {
			out = append(out, p.Name)
		}
	}
	return out
}

func (m *Model) keyInProfiles(k tea.KeyMsg) tea.Cmd {
	avail := m.sup.Config().AvailableProfiles
	switch k.String() {
	case "esc", "q", "p":
		m.mode = modeNormal
	case "up", "k", "ctrl+p":
		m.profIdx = max(0, m.profIdx-1)
	case "down", "j", "ctrl+n":
		m.profIdx = min(len(avail)-1, m.profIdx+1)
	case " ", "space", "x":
		if m.profIdx >= 0 && m.profIdx < len(avail) {
			n := avail[m.profIdx].Name
			m.profSel[n] = !m.profSel[n]
		}
	case "enter":
		m.mode = modeNormal
		names := m.chosenProfiles()
		if slices.Equal(names, m.sup.Config().Profiles) {
			return m.notify("profiles unchanged", theme.Active().Muted)
		}
		load := m.opts.LoadProfiles
		return func() tea.Msg {
			cfg, err := load(names)
			return profilesLoadedMsg{cfg: cfg, err: err}
		}
	}
	return nil
}

// offerProfiles shows what activating the chosen set would do, in the modal that applies an orbit.yaml change.
func (m *Model) offerProfiles(msg profilesLoadedMsg) tea.Cmd {
	if msg.err != nil {
		return m.notify("profiles: "+msg.err.Error(), theme.Active().Red)
	}
	diff := config.DiffConfig(m.sup.Config(), msg.cfg)
	if diff.IsEmpty() {
		return m.notify("profiles: nothing would change", theme.Active().Muted)
	}
	m.reload = &reloadOffer{diff: diff, cfg: msg.cfg, title: "Profiles", profiles: true}
	m.mode = modeReload
	return nil
}

func (m *Model) renderProfiles(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	avail := m.sup.Config().AvailableProfiles
	w := min(72, W-4)
	m.profIdx = max(0, min(m.profIdx, len(avail)-1))
	maxRows := max(1, min(14, H-8))
	start := windowAround(m.profIdx, maxRows, len(avail))
	end := min(len(avail), start+maxRows)

	nameW := 0
	for _, a := range avail {
		nameW = max(nameW, len([]rune(a.Name)))
	}
	nameW = min(nameW, 20)
	var content []string
	for i := start; i < end; i++ {
		a := avail[i]
		sel := i == m.profIdx
		bg := p.PanelAlt
		if sel {
			bg = p.Selection
		}
		box, boxColor := "[ ]", p.Dim
		if m.profSel[a.Name] {
			box, boxColor = "[x]", p.Green
		}
		l := newLine(bg)
		if sel {
			l.add(p.Accent, "▸ ").add(boxColor, box+" ").bold(p.Text, theme.Fit(a.Name, nameW))
		} else {
			l.add(p.Dim, "  ").add(boxColor, box+" ").add(p.Muted, theme.Fit(a.Name, nameW))
		}
		l.add(p.Dim, "  "+theme.Fit(a.Description, max(1, w-4-2-4-nameW-2)))
		content = append(content, l.pad(w-4).String())
	}
	content = append(content, newLine(p.PanelAlt).add(p.Dim, "↑↓ select · space toggle · enter review · esc close").String())
	h := end - start + 3
	x, y := center(W, H, w, h)
	return modalBox("Profiles", w, h, content), w, x, y
}
