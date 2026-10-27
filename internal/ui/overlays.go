package ui

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
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

var baseHelpRows = [][2]string{
	{"↑↓ / j k", "select service"},
	{"← → (graph)", "move across the dependency graph"},
	{"space", "start / stop selected (with dependencies)"},
	{"s · x · r", "start · stop · restart selected"},
	{"S · X · R", "start · stop · restart everything"},
	{"W", "pause / resume file watching of the selected service"},
	{"1 2 3 4", "dashboard · graph · logs · git"},
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
	{"(git changes)", "tree: space stage file/folder · a all · c commit · enter fold folder / open diff"},
	{"(git) f p u · L", "fetch · pull · push the selected repo · open it in lazygit"},
	{"(git repos) space a", "mark a repo for multi-repo actions · mark all / none"},
	{"(git) m then f p u b s", "multi-repo on the marked repos (all if none): fetch · pull · push · new branch · switch branch"},
	{"(git diff) [ ] { } s", "previous/next hunk · previous/next file · split view"},
	{"(git) y · Y", "copy: branch name · commit hash (Y: with subject) · stash ref · path"},
	{"(git diff) v", "copy mode: a cursor (j k) · v again starts the range · y copies with +/- · esc exits"},
	{"(git commands)", "j k move · y copy the command · c clear"},
	{"P · U", "open another project · apply orbit.yaml edits (restarts only what changed)"},
	{"p", "profiles of orbit.yaml: space toggles, enter shows what changes and asks before applying (not in the Git view, where p is pull)"},
	{"!", "list the tasks of orbit.yaml and run one (○ = it starts the services it needs first)"},
	{"T", "change color theme (live preview, enter saves)"},
	{"h", "dashboard: cpu / memory chart range 2m ⇄ 15m ⇄ 1h (memory turns orange/red near its limit, ↗ = leak)"},
	{"e", "environment variables of the selected service (j k move · v select a range · y copy value / KEY=value · Y always KEY=value · r reveal secrets)"},
	{"i", "interactive console: psql, rails console, container shell (ctrl+] hides it)"},
	{"o", "open service URL in the browser"},
	{": / ctrl+p", "command palette"},
	{"q", "quit: stop all services or leave them running"},
}

// helpRows are the rows of the keys modal: orbit's, then the ones the plugins add.
func (m *Model) helpRows() [][2]string {
	return append(slices.Clone(baseHelpRows), m.pluginHelpRows()...)
}

const helpKeyW = 24

// helpSize is the height of the keys modal and how many rows of it fit at a given terminal height.
func helpSize(rows, H int) (h, page int) {
	h = min(rows+3, max(5, H-4))
	return h, h - 3
}

func (m *Model) renderHelp(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	w := min(104, W-4)
	helpRows := m.helpRows()
	h, page := helpSize(len(helpRows), H)
	descW := max(1, w-4-helpKeyW)
	start := min(m.helpScroll, max(0, len(helpRows)-page))
	var content []string
	for _, r := range helpRows[start:min(len(helpRows), start+page)] {
		content = append(content, newLine(p.PanelAlt).add(p.Accent, theme.Fit(r[0], helpKeyW)).add(p.Text, theme.Fit(r[1], descW)).String())
	}
	foot := "press any key to close"
	if len(helpRows) > page {
		foot = fmt.Sprintf("j/k scroll · esc close  (%d-%d/%d)", start+1, start+page, len(helpRows))
	}
	content = append(content, newLine(p.PanelAlt).add(p.Dim, foot).String())
	x, y := center(W, H, w, h)
	return modalBox("Keys", w, h, content), w, x, y
}

var secretRe = regexp.MustCompile(`(?i)KEY|TOKEN|SECRET|PASSWORD|PASS|PWD|CREDENTIAL|PRIVATE`)

func isSecretKey(key string) bool { return secretRe.MatchString(key) }

// envScrollTo moves the scroll (in screen rows) the least that shows every row of a variable.
func envScrollTo(l envLayout, entry, scroll, page int) int {
	first, last := -1, -1
	for i, r := range l.rows {
		if r.entry == entry {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return scroll
	}
	if last >= scroll+page {
		scroll = last - page + 1
	}
	if first < scroll {
		scroll = first
	}
	return max(0, min(scroll, max(0, len(l.rows)-page)))
}

// envSelection is the range of variables a copy takes: just the one under the cursor, or what v marked.
func (m *Model) envSelection() (lo, hi int) {
	if m.envAnchor < 0 {
		return m.envCursor, m.envCursor
	}
	return min(m.envAnchor, m.envCursor), max(m.envAnchor, m.envCursor)
}

// envCopy copies the value under the cursor, or KEY=value for each variable of a range (or always with full).
// Secrets are copied for real even when the overlay hides them.
func (m *Model) envCopy(full bool) tea.Cmd {
	entries, _ := m.envEntries()
	lo, hi := m.envSelection()
	if lo < 0 || hi >= len(entries) {
		return m.notify("nothing to copy", theme.Active().Yellow)
	}
	var text string
	if lo == hi && !full {
		text = entries[lo].Value
	} else {
		var lines []string
		for _, e := range entries[lo : hi+1] {
			lines = append(lines, e.Key+"="+e.Value)
		}
		text = strings.Join(lines, "\n")
	}
	m.envAnchor = -1
	what, n := entries[lo].Key, hi-lo+1
	if n > 1 {
		what = fmt.Sprintf("%d variables", n)
	}
	return func() tea.Msg {
		return copyDoneMsg{res: clipboardCopy(text), what: what, n: n, unit: "variable"}
	}
}

// envPageSize is how many rows of the env overlay fit at a given terminal height.
func envPageSize(height int) int { return max(3, min(height-4, 30)-5) }

func (m *Model) envEntries() ([]config.EnvEntry, []string) {
	svc := m.sup.Service(m.selected)
	if svc == nil {
		return nil, nil
	}
	return config.ResolveEnv(svc.Env, svc.EnvFiles, m.sup.Config().Root)
}

// envRow is one screen row of the env overlay; a long value takes several, the continuation ones with no key.
type envRow struct {
	key, val, src string
	secret        bool
	entry         int // index of the variable this row belongs to
}

type envLayout struct {
	rows             []envRow
	keyW, srcW, valW int
}

// envLayoutFor lays the variables out for a terminal width: values are wrapped, never cut.
func (m *Model) envLayoutFor(W int) (envLayout, []string) {
	entries, missing := m.envEntries()
	w := min(100, W-4)
	l := envLayout{keyW: 8, srcW: 6}
	for _, e := range entries {
		l.keyW = max(l.keyW, min(32, len([]rune(e.Key))))
		l.srcW = max(l.srcW, min(24, len([]rune(e.Source))))
	}
	l.valW = max(8, w-4-l.keyW-l.srcW-2)
	flat := strings.NewReplacer("\r\n", "\\n", "\n", "\\n", "\r", "\\r")
	for i, e := range entries {
		if !m.envReveal && isSecretKey(e.Key) {
			l.rows = append(l.rows, envRow{key: e.Key, val: "••••••", src: e.Source, secret: true, entry: i})
			continue
		}
		val := []rune(flat.Replace(e.Value))
		first := true
		for {
			n := min(len(val), l.valW)
			row := envRow{val: string(val[:n]), entry: i}
			if first {
				row.key, row.src = e.Key, e.Source
			}
			l.rows = append(l.rows, row)
			val, first = val[n:], false
			if len(val) == 0 {
				break
			}
		}
	}
	return l, missing
}

func (m *Model) renderEnv(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	l, missing := m.envLayoutFor(W)
	entries, _ := m.envEntries()
	w := min(100, W-4)
	h := min(H-4, 30)
	page := envPageSize(H)
	start := min(m.envScroll, max(0, len(l.rows)-page))
	shown := l.rows[start:min(len(l.rows), start+page)]
	lo, hi := m.envSelection()
	var content []string
	if len(entries) == 0 {
		content = append(content, newLine(p.PanelAlt).add(p.Dim, "no variables defined for this service").String())
	}
	for _, r := range shown {
		bg := p.PanelAlt
		switch {
		case r.entry == m.envCursor:
			bg = p.Cursor
		case r.entry >= lo && r.entry <= hi && m.envAnchor >= 0:
			bg = p.Selection
		}
		color := p.Text
		if r.secret {
			color = p.Dim
		}
		content = append(content, newLine(bg).add(p.Accent, theme.Fit(r.key, l.keyW+1)).
			add(color, theme.Fit(r.val, l.valW+1)).add(p.Dim, theme.Fit(r.src, l.srcW)).String())
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
	foot := fmt.Sprintf("j/k move · v select · y copy · r %s secrets · esc close", verb)
	if m.envAnchor >= 0 {
		foot = fmt.Sprintf("%d selected · y copy KEY=value · v/esc cancel", hi-lo+1)
	}
	if len(entries) > 1 {
		foot += fmt.Sprintf("  (%d/%d)", m.envCursor+1, len(entries))
	}
	content = append(content, newLine(p.PanelAlt).add(p.Dim, foot).String())
	x, y := center(W, H, w, h)
	return modalBox(fmt.Sprintf("Env · %s (%d)", m.selected, len(entries)), w, h, content), w, x, y
}

func (m *Model) renderConfirm(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	w := min(76, W-4)
	busy := m.mode == modeStopping
	verb, title0, resume := "quit", "Quit", "reopen orbit to resume them"
	if m.pendingDir != "" {
		verb, title0, resume = "switch project", "Switch project", "reopen that project to resume them"
	}
	msg := fmt.Sprintf("%d service(s) running.", m.sup.OwnedRunningCount())
	for _, n := range m.names() {
		if m.sup.IsAdopted(n) {
			msg += " (attached containers stay up)"
			break
		}
	}
	tasksMsg := ""
	if n := m.runningTasks(); n > 0 {
		tasksMsg = fmt.Sprintf("%d task(s) running: they stop either way.", n)
		if m.sup.OwnedRunningCount() == 0 {
			msg, tasksMsg = tasksMsg, ""
		}
	}
	title, h := title0, 7
	if tasksMsg != "" {
		h++
	}
	var content []string
	running := m.sup.OwnedRunningCount()
	if busy && (m.busyHow == "detach" || running == 0) {
		// nothing is being stopped: the services stay up, only the project changes
		title, h = title0, 6
		content = []string{
			newLine(p.PanelAlt).add(p.Text, "Opening "+filepath.Base(m.pendingDir)+"…").String(),
		}
		if running > 0 {
			content = append(content, newLine(p.PanelAlt).add(p.Dim, fmt.Sprintf("leaving %d service(s) running", running)).String())
		}
	} else if busy {
		title, h = "Stopping", 6
		content = []string{
			newLine(p.PanelAlt).add(p.Text, fmt.Sprintf("Stopping %d service(s)…", running)).String(),
			newLine(p.PanelAlt).add(p.Dim, "please wait…").String(),
		}
	} else {
		content = []string{newLine(p.PanelAlt).add(p.Text, msg).String()}
		if tasksMsg != "" {
			content = append(content, newLine(p.PanelAlt).add(p.Orange, tasksMsg).String())
		}
		content = append(content,
			newLine(p.PanelAlt).add(p.Green, "s").add(p.Dim, " stop all & "+verb).String(),
			newLine(p.PanelAlt).add(p.Accent, "d").add(p.Dim, " leave running & "+verb+" ("+resume+")").String(),
			newLine(p.PanelAlt).add(p.Red, "n / esc").add(p.Dim, " cancel").String())
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
	notNow := " not now (U applies later)"
	if m.reload.profiles {
		notNow = " cancel"
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
		newLine(p.PanelAlt).add(p.Red, "n / esc").add(p.Dim, notNow).String(),
	)
	h := shown + 7
	x, y := center(W, H, w, h)
	title := "orbit.yaml changed"
	if m.reload.title != "" {
		title = m.reload.title
	}
	return modalBox(title, w, h, content), w, x, y
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
	footer := newLine(p.Bg).add(p.Dim, theme.Fit(" ctrl+] hide (keeps running) · exit / ctrl+d end the session", cols))
	switch {
	case c.exited && c.code == 0:
		footer = newLine(p.Bg).add(p.Green, theme.Fit(fmt.Sprintf(" ✓ done in %.1fs · any key to close", c.elapsed.Seconds()), cols))
	case c.exited:
		footer = newLine(p.Bg).add(p.Red, theme.Fit(fmt.Sprintf(" ✖ exit %d after %.1fs · any key to close", c.code, c.elapsed.Seconds()), cols))
	case c.task:
		footer = newLine(p.Bg).add(p.Dim, theme.Fit(" ctrl+] hide (keeps running) · ctrl+c stops the task", cols))
	}
	content = append(content, footer.String())
	box := panel(panelOpts{title: fmt.Sprintf("› %s · %s", c.name, c.session.Spec.Title), focused: true, bg: p.Bg}, w, h, content)
	x, y := center(W, H, w, h)
	return box, w, x, y
}
