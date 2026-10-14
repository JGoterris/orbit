package ui

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// fuzzyScore is a subsequence fuzzy match; lower is better, ok false means no match.
func fuzzyScore(query, text string) (int, bool) {
	if query == "" {
		return 0, true
	}
	q, t := strings.ToLower(query), strings.ToLower(text)
	if i := strings.Index(t, q); i >= 0 {
		return i, true
	}
	score := 100
	rt := []rune(t)
	ti := 0
	for _, ch := range q {
		if ch == ' ' {
			continue
		}
		found := -1
		for j := ti; j < len(rt); j++ {
			if rt[j] == ch {
				found = j
				break
			}
		}
		if found < 0 {
			return 0, false
		}
		score += found - ti
		ti = found + 1
	}
	return score, true
}

// command is one entry of the command palette.
type command struct {
	id    string
	label string
	hint  string
	run   func(m *Model) tea.Cmd
}

func filterCommands(cmds []command, query string) []command {
	type scored struct {
		c command
		s int
		i int
	}
	var list []scored
	for i, c := range cmds {
		if s, ok := fuzzyScore(query, c.label); ok {
			list = append(list, scored{c, s, i})
		}
	}
	// stable: equal scores keep the order of the list
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].s < list[j-1].s; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	out := make([]command, len(list))
	for i, s := range list {
		out[i] = s.c
	}
	return out
}

// windowAround returns the first index of a window of `size` rows that keeps `selected` roughly in the middle.
func windowAround(selected, size, total int) int {
	return max(0, min(selected-size/2, total-size))
}

func (m *Model) renderPalette(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	w := min(72, W-4)
	maxRows := 14
	matches := filterCommands(m.commands(), m.palette.value())
	start := windowAround(m.paletteIdx, maxRows, len(matches))
	var content []string
	content = append(content, newLine(p.PanelAlt).add(p.Accent, "› ").
		raw(m.palette.viewWithPlaceholder(w-6, "type a command or service…", p.Text, p.Dim, p.PanelAlt, true), w-6).String())
	content = append(content, newLine(p.PanelAlt).add(p.Border, strings.Repeat("─", w-4)).String())
	if len(matches) == 0 {
		content = append(content, newLine(p.PanelAlt).add(p.Dim, "no matching commands").String())
	}
	for i := start; i < len(matches) && i < start+maxRows; i++ {
		c := matches[i]
		sel := i == m.paletteIdx
		bg := p.PanelAlt
		if sel {
			bg = p.Selection
		}
		l := newLine(bg)
		if sel {
			l.add(p.Accent, "▸ ").add(p.Text, theme.Fit(c.label, w-8-len([]rune(c.hint))))
		} else {
			l.add(p.Dim, "  ").add(p.Muted, theme.Fit(c.label, w-8-len([]rune(c.hint))))
		}
		l.add(p.Dim, c.hint)
		content = append(content, l.pad(w-4).String())
	}
	h := maxRows + 5
	x, y := center(W, H, w, h)
	return modalBox("Command palette", w, h, content), w, x, y
}

var swatchKeys = []string{"Accent", "Accent2", "Cyan", "Green", "Yellow", "Orange", "Red"}

func swatch(pal theme.Palette) []string {
	return []string{pal.Accent, pal.Accent2, pal.Cyan, pal.Green, pal.Yellow, pal.Orange, pal.Red}
}

func (m *Model) renderThemePicker(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	w := min(60, W-4)
	maxRows := max(1, min(14, H-6))
	start := windowAround(m.themeIdx, maxRows, len(m.themeEntries))
	end := min(len(m.themeEntries), start+maxRows)
	nameW := max(1, w-4-2-len(swatchKeys)-9)
	var content []string
	for i := start; i < end; i++ {
		e := m.themeEntries[i]
		sel := i == m.themeIdx
		bg := p.PanelAlt
		if sel {
			bg = p.Selection
		}
		l := newLine(bg)
		label := e.name
		if e.custom {
			label += " (custom)"
		}
		if sel {
			l.add(p.Accent, "▸ ").add(p.Text, theme.Fit(label, nameW))
		} else {
			l.add(p.Dim, "  ").add(p.Muted, theme.Fit(label, nameW))
		}
		if e.name == m.savedTheme {
			l.add(p.Green, "✓ ")
		} else {
			l.add(p.Green, "  ")
		}
		for _, c := range swatch(e.palette) {
			l.add(c, "■")
		}
		content = append(content, l.pad(w-4).String())
	}
	content = append(content, newLine(p.PanelAlt).add(p.Dim, "↑↓ preview · enter save · esc cancel").String())
	h := end - start + 4
	x, y := center(W, H, w, h)
	return modalBox("Theme", w, h, content), w, x, y
}

var helpRows = [][2]string{
	{"↑↓ / j k", "select service"},
	{"← → (graph)", "move across the dependency graph"},
	{"space", "start / stop selected (with dependencies)"},
	{"s · x · r", "start · stop · restart selected"},
	{"S · X · R", "start · stop · restart everything"},
	{"W", "pause / resume file watching of the selected service"},
	{"1 2 3", "dashboard · graph · logs"},
	{"tab / shift+tab", "move focus between panels"},
	{"z · esc", "zoom the focused panel · back"},
	{"+ - =", "resize the focused panel · reset"},
	{"enter / l", "logs of the selected service"},
	{"a", "logs: toggle selected ⇄ all services"},
	{"/", "filter logs (regex)  ·  esc clears"},
	{"f · pgup pgdn · wheel", "follow · scroll logs"},
	{"(logs focused)", "j k · ctrl+u/d · g G  scroll"},
	{"t · w · c", "toggle timestamps · line wrap · clear logs"},
	{"b", "fold / unfold all stack traces (enter/space in copy mode: just the one under the cursor)"},
	{"/ then tab", "filter bar ⇄ search bar (highlight, keep all lines)"},
	{"n · N", "next (newer) · previous (older) search match"},
	{"v (logs focused)", "copy mode: j k ctrl+u/d move, v select, enter/space on a trace opens it, y copy"},
	{"Y · E", "copy all visible logs · export them to a file"},
	{"P · U", "open another project · apply orbit.yaml edits (restarts only what changed)"},
	{"T", "change color theme (live preview, enter saves)"},
	{"h", "dashboard: cpu / memory chart range 2m ⇄ 15m ⇄ 1h (memory turns orange/red near its limit, ↗ = leak)"},
	{"e", "environment variables of the selected service"},
	{"i", "interactive console: psql, rails console, container shell (ctrl+] hides it)"},
	{"o", "open service URL in the browser"},
	{": / ctrl+p", "command palette"},
	{"q", "quit: stop all services or leave them running"},
}

const helpKeyW = 24

func (m *Model) renderHelp(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	w := min(104, W-4)
	h := min(len(helpRows)+3, max(4, H-2))
	descW := max(1, w-4-helpKeyW)
	var content []string
	for _, r := range helpRows[:min(len(helpRows), h-3)] {
		content = append(content, newLine(p.PanelAlt).add(p.Accent, theme.Fit(r[0], helpKeyW)).add(p.Text, theme.Fit(r[1], descW)).String())
	}
	foot := "press any key to close"
	if h-3 < len(helpRows) {
		foot = "terminal too short: enlarge it to see all keys"
	}
	content = append(content, newLine(p.PanelAlt).add(p.Dim, foot).String())
	x, y := center(W, H, w, h)
	return modalBox("Keys", w, h, content), w, x, y
}

var secretRe = regexp.MustCompile(`(?i)KEY|TOKEN|SECRET|PASSWORD|PASS|PWD|CREDENTIAL|PRIVATE`)

func isSecretKey(key string) bool { return secretRe.MatchString(key) }

// envPageSize is how many rows of the env overlay fit at a given terminal height.
func envPageSize(height int) int { return max(3, min(height-4, 30)-5) }

func (m *Model) envEntries() ([]config.EnvEntry, []string) {
	svc := m.sup.Service(m.selected)
	if svc == nil {
		return nil, nil
	}
	return config.ResolveEnv(svc.Env, svc.EnvFiles, m.sup.Config().Root)
}

func (m *Model) renderEnv(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	entries, missing := m.envEntries()
	w := min(100, W-4)
	h := min(H-4, 30)
	page := envPageSize(H)
	start := min(m.envScroll, max(0, len(entries)-page))
	shown := entries[start:min(len(entries), start+page)]
	keyW, srcW := 8, 6
	for _, e := range entries {
		keyW = max(keyW, min(32, len([]rune(e.Key))))
		srcW = max(srcW, min(24, len([]rune(e.Source))))
	}
	valW := max(8, w-4-keyW-srcW-2)
	var content []string
	if len(shown) == 0 {
		content = append(content, newLine(p.PanelAlt).add(p.Dim, "no variables defined for this service").String())
	}
	for _, e := range shown {
		val := e.Value
		color := p.Text
		if !m.envReveal && isSecretKey(e.Key) {
			val, color = "••••••", p.Dim
		}
		content = append(content, newLine(p.PanelAlt).add(p.Accent, theme.Fit(e.Key, keyW+1)).
			add(color, theme.Fit(val, valW+1)).add(p.Dim, theme.Fit(e.Source, srcW)).String())
	}
	for len(content) < h-2-2 {
		content = append(content, blank(w-4, p.PanelAlt))
	}
	if len(missing) > 0 {
		content = append(content, newLine(p.PanelAlt).add(p.Yellow, theme.Fit("missing env_file: "+strings.Join(missing, ", "), w-4)).String())
	}
	verb := "reveal"
	if m.envReveal {
		verb = "hide"
	}
	foot := fmt.Sprintf("j/k scroll · v %s secrets · esc close", verb)
	if len(entries) > page {
		foot += fmt.Sprintf("  (%d-%d/%d)", start+1, start+len(shown), len(entries))
	}
	content = append(content, newLine(p.PanelAlt).add(p.Dim, foot).String())
	x, y := center(W, H, w, h)
	return modalBox(fmt.Sprintf("Env · %s (%d)", m.selected, len(entries)), w, h, content), w, x, y
}

func (m *Model) renderConfirm(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	w := min(60, W-4)
	busy := m.mode == modeStopping
	verb, title0 := "quit", "Quit"
	if m.pendingDir != "" {
		verb, title0 = "switch project", "Switch project"
	}
	msg := fmt.Sprintf("%d service(s) running.", m.sup.OwnedRunningCount())
	for _, n := range m.names() {
		if m.sup.IsAdopted(n) {
			msg += " (attached containers stay up)"
			break
		}
	}
	title, h := title0, 7
	var content []string
	if busy {
		title, h = "Stopping", 6
		content = []string{
			newLine(p.PanelAlt).add(p.Text, fmt.Sprintf("Stopping %d service(s)…", m.sup.OwnedRunningCount())).String(),
			newLine(p.PanelAlt).add(p.Dim, "please wait…").String(),
		}
	} else {
		content = []string{
			newLine(p.PanelAlt).add(p.Text, msg).String(),
			newLine(p.PanelAlt).add(p.Green, "s").add(p.Dim, " stop all & "+verb).String(),
			newLine(p.PanelAlt).add(p.Accent, "d").add(p.Dim, " leave running & "+verb+" (reopen that project to resume them)").String(),
			newLine(p.PanelAlt).add(p.Red, "n / esc").add(p.Dim, " cancel").String(),
		}
	}
	x, y := center(W, H, w, h)
	return modalBox(title, w, h, content), w, x, y
}

func (m *Model) renderReload(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	w := min(72, W-4)
	lines := m.reload.diff.Describe()
	shown := max(1, min(len(lines), H-10))
	var content []string
	for _, l := range lines[:min(len(lines), shown)] {
		c := p.Accent
		switch {
		case strings.HasPrefix(l, "+"):
			c = p.Green
		case strings.HasPrefix(l, "-"):
			c = p.Red
		case strings.HasPrefix(l, "!"):
			c = p.Yellow
		}
		content = append(content, newLine(p.PanelAlt).add(c, theme.Fit(l, w-4)).String())
	}
	if len(lines) > shown {
		content = append(content, newLine(p.PanelAlt).add(p.Dim, fmt.Sprintf("… and %d more", len(lines)-shown)).String())
	}
	restarts := 0
	for _, c := range m.reload.diff.Changed {
		if c.Restart {
			restarts++
		}
	}
	content = append(content,
		newLine(p.PanelAlt).add(p.Dim, fmt.Sprintf("%d restart%s (↻) · only running services are restarted", restarts, plural(restarts))).String(),
		newLine(p.PanelAlt).add(p.Green, "y / enter").add(p.Dim, " apply").String(),
		newLine(p.PanelAlt).add(p.Red, "n / esc").add(p.Dim, " not now (U applies later)").String(),
	)
	h := shown + 7
	x, y := center(W, H, w, h)
	return modalBox("orbit.yaml changed", w, h, content), w, x, y
}

// consoleSize is the size of the floating console and of the terminal inside it (border and footer excluded).
func consoleSize(width, height int) (w, h, cols, rows int) {
	w = max(20, width*9/10)
	h = max(6, height*85/100)
	return w, h, w - 2, h - 3
}

func (m *Model) renderConsole(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	w, h, cols, rows := consoleSize(W, H)
	c := m.console
	lines := c.term.StyledLines(true)
	content := make([]string, 0, rows+1)
	for i := 0; i < rows; i++ {
		if i < len(lines) {
			content = append(content, lines[i])
		} else {
			content = append(content, blank(cols, p.Bg))
		}
	}
	content = append(content, newLine(p.Bg).add(p.Dim, theme.Fit(" ctrl+] hide (keeps running) · exit / ctrl+d end the session", cols)).String())
	box := panel(panelOpts{title: fmt.Sprintf("› %s · %s", c.name, c.session.Spec.Title), focused: true, bg: p.Bg}, w, h, content)
	x, y := center(W, H, w, h)
	return box, w, x, y
}
