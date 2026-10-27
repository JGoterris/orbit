package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/console"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/metrics"
	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/core/tasks"
	"github.com/JGoterris/orbit/internal/proc"
	"github.com/JGoterris/orbit/internal/ui/term"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// notify shows a message in the footer for a few seconds.
func (m *Model) notify(text, color string) tea.Cmd {
	m.toastID++
	id := m.toastID
	if color == "" {
		color = theme.Active().Text
	}
	m.toast = &toast{id: id, text: text, color: color}
	return tea.Tick(3500*time.Millisecond, func(time.Time) tea.Msg { return toastExpireMsg{id} })
}

func (m *Model) clearToast() { m.toast = nil }

// op runs a supervisor operation off the UI loop and asks for a refresh when it ends.
func (m *Model) op(fn func()) tea.Cmd {
	return func() tea.Msg {
		fn()
		return refreshMsg{}
	}
}

func (m *Model) moveSelection(delta int) {
	names := m.names()
	if len(names) == 0 {
		return
	}
	i := 0
	for j, n := range names {
		if n == m.selected {
			i = j
		}
	}
	m.selected = names[((i+delta)%len(names)+len(names))%len(names)]
	m.scrollBack = 0
}

func (m *Model) setSelected(name string) {
	if name != "" && name != m.selected {
		m.selected = name
		m.scrollBack = 0
	}
}

func (m *Model) setView(id string) {
	if id == m.view {
		return
	}
	m.view = id
	m.focus = m.viewByID(id).defaultPane
	m.zoomed = false
	m.scrollBack = 0
	if id == "git" {
		// the Git view opens on the repo of the service you were looking at, and reads every repo right away
		m.pendingGit = true
	}
}

func (m *Model) toggleWatch(name string) tea.Cmd {
	res := m.sup.ToggleWatch(name)
	p := theme.Active()
	switch res {
	case "":
		return m.notify(fmt.Sprintf("%s has no watch in orbit.yaml", name), p.Yellow)
	case "paused":
		return m.notify("watch paused: "+name, p.Yellow)
	}
	return m.notify("watch resumed: "+name, p.Accent)
}

func (m *Model) openService(name string) tea.Cmd {
	svc := m.sup.Service(name)
	p := theme.Active()
	if svc == nil {
		return nil
	}
	url := svc.URL
	if url == "" && svc.Port != 0 {
		url = fmt.Sprintf("http://localhost:%d", svc.Port)
	}
	if url == "" {
		return m.notify(name+" has no port or url", p.Yellow)
	}
	return tea.Batch(m.notify("opening "+url, p.Accent), func() tea.Msg { return openURLMsg{url, proc.OpenURL(url)} })
}

// ---------------------------------------------------------------- logs

func (m *Model) clearLogQueries() {
	m.clearToast() // a lingering "match 3/17" would keep hiding the key hints
	m.filter, m.search, m.match = "", "", 0
	m.bar = input{}
}

func (m *Model) exitCopy() {
	m.copyCursor, m.copyAnchor = 0, 0
	m.mode = modeNormal
}

func (m *Model) toggleTrace(seq int) {
	if m.expanded[seq] {
		delete(m.expanded, seq)
	} else {
		m.expanded[seq] = true
	}
}

func (m *Model) copyLines(lines []logs.Line, what string) tea.Cmd {
	if len(lines) == 0 {
		return m.notify("nothing to copy", theme.Active().Yellow)
	}
	text := logs.FormatLines(lines, m.showTime, m.logService() == "")
	n := len(lines)
	return func() tea.Msg { return copyDoneMsg{res: clipboardCopy(text), what: what, n: n} }
}

func (m *Model) exportLogs() tea.Cmd {
	lines := m.rawViewLines()
	if len(lines) == 0 {
		return m.notify("no logs to export", theme.Active().Yellow)
	}
	dir := filepath.Join(m.sup.StateDir(), "exports")
	service := m.logService()
	label := service
	if label == "" {
		label = "all"
	}
	text := logs.FormatLines(lines, true, service == "") + "\n"
	n := len(lines)
	return func() tea.Msg {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return exportDoneMsg{err: err}
		}
		file := filepath.Join(dir, fmt.Sprintf("%s-%s.log", label, time.Now().Format("20060102-150405")))
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			return exportDoneMsg{err: err}
		}
		clipboardCopy(file) // the path is what you want next (open it, attach it, paste it in an issue)
		return exportDoneMsg{path: file, lines: n}
	}
}

// jumpMatch moves to the next (newer) / previous (older) line matching the search; the first jump goes to the newest.
func (m *Model) jumpMatch(dir int, fromEnd bool) tea.Cmd {
	p := theme.Active()
	if m.search == "" {
		return m.notify("no search: press /, tab, type, enter", p.Yellow)
	}
	lines := m.rawViewLines()
	hit := logs.Matcher(m.search)
	var idxs []int
	for i, l := range lines {
		if hit(l) {
			idxs = append(idxs, i)
		}
	}
	if len(idxs) == 0 {
		return m.notify(fmt.Sprintf("no matches for /%s", m.search), p.Yellow)
	}
	at := -1
	if m.match != 0 && !fromEnd {
		for i, l := range lines {
			if l.Seq == m.match {
				at = i
			}
		}
	}
	k := len(idxs) - 1
	if at >= 0 {
		after, before := -1, -1
		for j, i := range idxs {
			if i > at && after < 0 {
				after = j
			}
			if i < at {
				before = j
			}
		}
		switch {
		case dir > 0 && after < 0:
			k = 0
		case dir > 0:
			k = after
		case before < 0:
			k = len(idxs) - 1
		default:
			k = before
		}
	}
	found := lines[idxs[k]]
	m.match = found.Seq
	// a match inside a folded stack trace opens it
	if m.fold && found.TraceOf != 0 && !m.expanded[found.TraceOf] {
		m.toggleTrace(found.TraceOf)
	}
	m.revealSeq(found.Seq)
	return m.notify(fmt.Sprintf("match %d/%d", k+1, len(idxs)), p.Accent)
}

// revealSeq scrolls the log panel so the line is visible.
func (m *Model) revealSeq(seq int) {
	lines := m.viewLines()
	idx := -1
	for i, l := range lines {
		if l.Seq == seq {
			idx = i
		}
	}
	if idx < 0 {
		return
	}
	m.geo.mu.Lock()
	w, h := m.geo.logW, m.geo.logH
	m.geo.mu.Unlock()
	if w == 0 || h == 0 {
		return
	}
	lay := m.newLogLayout(lines, w, h, m.logService() != "")
	first, end, _, maxBack := lay.window(m)
	switch {
	case idx < first:
		m.scrollBack = min(maxBack, m.scrollBack+first-idx)
	case idx >= end:
		m.scrollBack = max(0, m.scrollBack-(idx-end+1))
	}
}

func (m *Model) clampScroll() {
	m.geo.mu.Lock()
	maxBack := m.geo.maxBack
	m.geo.mu.Unlock()
	if m.scrollBack > maxBack && maxBack >= 0 && m.geo.logH > 0 {
		m.scrollBack = maxBack
	}
	if m.scrollBack < 0 {
		m.scrollBack = 0
	}
}

func (m *Model) scrollBy(delta int) {
	m.geo.mu.Lock()
	maxBack := m.geo.maxBack
	known := m.geo.logH > 0
	m.geo.mu.Unlock()
	m.scrollBack += delta
	if m.scrollBack < 0 {
		m.scrollBack = 0
	}
	if known && m.scrollBack > maxBack {
		m.scrollBack = maxBack
	}
}

// ---------------------------------------------------------------- reload, theme, console, quit

func (m *Model) openReload() tea.Cmd {
	p := theme.Active()
	pending := m.sup.Pending()
	switch {
	case pending != nil && pending.Error != "":
		return m.notify("orbit.yaml does not load: "+pending.Error, p.Red)
	case pending == nil || pending.Diff == nil:
		return m.notify("orbit.yaml: no changes to apply", p.Muted)
	}
	m.reload = &reloadOffer{diff: *pending.Diff}
	m.mode = modeReload
	return nil
}

func (m *Model) applyReload() tea.Cmd {
	offer := m.reload
	m.reload = nil
	m.mode = modeNormal
	if offer == nil {
		return nil
	}
	return func() tea.Msg {
		diff, err := m.sup.Reload(offer.cfg)
		if err == nil && offer.profiles && m.opts.SaveProfiles != nil {
			m.opts.SaveProfiles(offer.cfg.Profiles)
		}
		return reloadDoneMsg{diff: diff, err: err}
	}
}

func (m *Model) previewTheme(index int) {
	if len(m.themeEntries) == 0 {
		return
	}
	index = max(0, min(len(m.themeEntries)-1, index))
	e := m.themeEntries[index]
	m.themeIdx = index
	theme.Apply(e.palette)
	m.themeName = e.name
}

func (m *Model) openThemePicker() {
	m.themeIdx = 0
	for i, e := range m.themeEntries {
		if e.name == m.themeName {
			m.themeIdx = i
		}
	}
	m.mode = modeTheme
}

func (m *Model) saveTheme() tea.Cmd {
	m.savedTheme = m.themeName
	m.mode = modeNormal
	p := theme.Active()
	if m.opts.SaveTheme == nil || m.opts.SaveTheme(m.themeName) {
		return m.notify("theme: "+m.themeName, p.Accent)
	}
	return m.notify(fmt.Sprintf("theme: %s (could not save it to the config file)", m.themeName), p.Yellow)
}

func (m *Model) cancelTheme() {
	for _, e := range m.themeEntries {
		if e.name == m.savedTheme {
			theme.Apply(e.palette)
			m.themeName = e.name
		}
	}
	m.mode = modeNormal
}

func (m *Model) openConsole(name string) tea.Cmd {
	p := theme.Active()
	svc := m.sup.Service(name)
	if svc == nil {
		return nil
	}
	st := m.sup.State(name)
	spec, err := console.Command(svc, console.ServiceState{Status: string(st.Status), ContainerID: st.ContainerID})
	if err != nil {
		return m.notify(err.Error(), p.Yellow)
	}
	return m.attachConsole(name, spec, false)
}

// attachConsole opens (or reattaches to) the session under key and shows it in the floating console.
func (m *Model) attachConsole(key string, spec console.Spec, task bool) tea.Cmd {
	p := theme.Active()
	_, _, cols, rows := consoleSize(m.width, m.height)
	session, err := m.consoles.Open(key, spec, cols, rows)
	if err != nil {
		return m.notify("console: "+err.Error(), p.Red)
	}
	cs := &consoleState{name: key, session: session, cols: cols, rows: rows, task: task, started: time.Now()}
	cs.term = term.New(cols, rows, writerFunc(func(b []byte) { session.Write(b) }))
	cs.detach = session.Attach(func(b []byte) { cs.term.Write(b) })
	session.Resize(cols, rows)
	m.console = cs
	m.mode = modeConsole
	return m.watchConsole(cs)
}

// runTask runs a task of orbit.yaml in the floating console, after the services it depends on are ready.
func (m *Model) runTask(name string) tea.Cmd {
	t := m.sup.Config().Tasks[name]
	if t == nil {
		return nil
	}
	if len(t.DependsOn) == 0 {
		return m.openTask(t)
	}
	sup, deps := m.sup, t.DependsOn
	return tea.Batch(m.notify("task "+name+": starting "+strings.Join(deps, ", "), theme.Active().Muted), func() tea.Msg {
		ok := true
		for _, d := range deps {
			if !sup.Start(d) {
				ok = false
			}
		}
		return taskDepsMsg{name: name, ok: ok}
	})
}

func (m *Model) openTask(t *config.TaskConfig) tea.Cmd {
	spec, err := tasks.Command(t, nil)
	if err != nil {
		return m.notify("task "+t.Name+": "+err.Error(), theme.Active().Red)
	}
	return m.attachConsole("task:"+t.Name, console.Spec{Argv: spec.Argv, Cwd: spec.Cwd, Env: spec.Env, Title: spec.Title}, true)
}

type writerFunc func([]byte)

func (f writerFunc) Write(b []byte) (int, error) { f(b); return len(b), nil }

// watchConsole tells the UI when the program of the console ends (exit, ctrl+d, crash).
func (m *Model) watchConsole(cs *consoleState) tea.Cmd {
	return func() tea.Msg {
		<-cs.session.Done()
		return consoleExitMsg{name: cs.name, code: cs.session.ExitCode(), elapsed: time.Since(cs.started)}
	}
}

func (m *Model) closeConsole() {
	if m.console != nil {
		m.console.detach()
		m.console.term.Close()
		m.consoles.Discard(m.console.name)
	}
	m.console = nil
	m.mode = modeNormal
}

// runningTasks is how many tasks are still running, in the console or hidden behind it.
func (m *Model) runningTasks() int { return m.consoles.Running("task:") }

func (m *Model) requestQuit() tea.Cmd {
	if m.sup.OwnedRunningCount() == 0 && m.runningTasks() == 0 {
		return m.doQuit("stop")
	}
	m.mode = modeQuit
	return nil
}

func (m *Model) doQuit(how string) tea.Cmd {
	if how == "stop" {
		m.mode = modeStopping
		m.busyHow = "stop"
	}
	m.consoles.CloseAll()
	return func() tea.Msg {
		if m.opts.OnQuit != nil {
			m.opts.OnQuit(how)
		}
		return quitDoneMsg{}
	}
}

// loadHistory fetches the buckets of the 15m / 1h charts (the 2m range uses the live samples).
// loadHistory is called whenever the view or the selected service may have changed: it loads what the new screen
// needs, the resource history and what the plugins show.
func (m *Model) loadHistory() tea.Cmd {
	return tea.Batch(m.loadHistoryData(), m.pluginOnSelection())
}

func (m *Model) loadHistoryData() tea.Cmd {
	if m.resRange == resources.Range2m || m.view != "dashboard" || m.selected == "" {
		return nil
	}
	if m.historyFor == m.selected && m.historyRng == m.resRange && time.Since(m.historyAt) < 10*time.Second {
		return nil
	}
	name, rng := m.selected, m.resRange
	m.historyFor, m.historyRng, m.historyAt = name, rng, time.Now()
	return func() tea.Msg {
		b, _ := m.sup.HistoryOf(name, resources.RangeMs[rng])
		return historyMsg{name: name, rng: rng, buckets: b}
	}
}

func (m *Model) memToast(name string, st supervisor.ServiceState, prev resReport) tea.Cmd {
	p := theme.Active()
	r := st.Resources
	var cmds []tea.Cmd
	if r != nil && r.Level != resources.LevelNone && r.Level != prev.level && r.MemLimit > 0 {
		mem := 0.0
		if len(st.Mem) > 0 {
			mem = st.Mem[len(st.Mem)-1]
		}
		if r.Level == resources.LevelOver {
			cmds = append(cmds, m.notify(fmt.Sprintf("✖ %s: memory over its limit (%s / %s)", name, metrics.FormatBytes(mem), metrics.FormatBytes(float64(r.MemLimit))), p.Red))
		} else {
			cmds = append(cmds, m.notify(fmt.Sprintf("▲ %s: memory at %d%% of %s", name, int(mem/float64(r.MemLimit)*100+0.5), metrics.FormatBytes(float64(r.MemLimit))), p.Orange))
		}
	}
	if r != nil && r.Leak != nil && !prev.leak {
		cmds = append(cmds, m.notify(fmt.Sprintf("↗ %s: possible memory leak (+%s/min)", name, metrics.FormatBytes(r.Leak.PerMin)), p.Orange))
	}
	return tea.Batch(cmds...)
}

type resReport struct {
	level resources.MemLevel
	leak  bool
}
