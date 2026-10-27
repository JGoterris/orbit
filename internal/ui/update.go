package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// Init starts the clock and shows what is waiting for the user at startup.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickEvery(250 * time.Millisecond)}
	if m.opts.Splash {
		m.splash = newSplash()
		cmds = append(cmds, splashTick())
	}
	switch r := m.opts.Relaunch; {
	case m.opts.StartWithPicker:
		m.openProjects()
	case r != nil:
		m.reload = &reloadOffer{diff: r.Diff, cfg: r.Config}
		m.mode = modeReload
	}
	for _, info := range m.plug.infos {
		if info.Missing {
			cmds = append(cmds, m.notify(fmt.Sprintf("this project uses plugin %s, which is not installed: %s", info.Name, info.Install), theme.Active().Yellow))
		}
	}
	if errs := m.opts.ThemeErrors; len(errs) > 0 {
		text := errs[0]
		if len(errs) > 1 {
			text += " (+" + itoa(len(errs)-1) + " more)"
		}
		cmds = append(cmds, m.notify(text, theme.Active().Yellow))
	}
	return tea.Batch(cmds...)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// afterRefresh keeps the model consistent with a supervisor that changed under it.
func (m *Model) afterRefresh() tea.Cmd {
	names := m.names()
	found := false
	for _, n := range names {
		if n == m.selected {
			found = true
		}
	}
	if !found {
		m.selected = ""
		if len(names) > 0 {
			m.selected = names[0]
		}
	}
	// while scrolled up, new lines must not move what is being read (or copied): compensate the offset
	key := m.logService() + "|" + m.view
	lines := m.viewLines()
	newest := 0
	if len(lines) > 0 {
		newest = lines[len(lines)-1].Seq
	}
	if m.seen.key == key && newest != m.seen.seq {
		added := 0
		for i := len(lines) - 1; i >= 0 && lines[i].Seq > m.seen.seq; i-- {
			added++
		}
		if (m.scrollBack > 0 || m.mode == modeCopy) && added > 0 {
			m.scrollBack += added
			m.unread += added
		}
	}
	m.seen.key, m.seen.seq = key, newest
	if m.scrollBack == 0 {
		m.unread = 0
	}
	m.clampScroll()
	// the pending config badge and its toast
	var cmds []tea.Cmd
	cmds = append(cmds, m.loadHistory())
	return tea.Batch(cmds...)
}

func (m *Model) stateChanged(name string) tea.Cmd {
	if name == "" {
		return nil
	}
	p := theme.Active()
	st := m.sup.State(name)
	var cmds []tea.Cmd
	cmds = append(cmds, m.memToast(name, st, m.lastRes[name]))
	m.lastRes[name] = resourceReport(st)
	prev := m.lastStatus[name]
	m.lastStatus[name] = st.Status
	if prev != st.Status {
		switch st.Status {
		case supervisor.Crashed, supervisor.Failed:
			msg := st.Error
			if msg == "" {
				msg = string(st.Status)
			}
			cmds = append(cmds, m.notify("✖ "+name+": "+msg, p.Red))
		case supervisor.Unhealthy:
			text := "● " + name + " is unhealthy"
			if st.Health != "" {
				text += ": " + st.Health
			}
			cmds = append(cmds, m.notify(text, p.Orange))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) pendingChanged() tea.Cmd {
	p := theme.Active()
	pend := m.sup.Pending()
	desc := ""
	switch {
	case pend == nil:
	case pend.Error != "":
		desc = "e:" + pend.Error
	case pend.Diff != nil:
		desc = "d:" + strings.Join(pend.Diff.Describe(), "\n")
	}
	if desc == m.pendingSeen {
		return nil
	}
	m.pendingSeen = desc
	switch {
	case pend == nil:
		return nil
	case pend.Error != "":
		return m.notify("✖ orbit.yaml does not load: "+pend.Error, p.Red)
	case pend.Diff != nil:
		return m.notify("⟳ orbit.yaml changed — U to apply", p.Orange)
	}
	return nil
}

// Update handles every message of the program, then asks for the diff of whatever the Git view has selected.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if nm, ok := next.(*Model); ok {
		if nm.pendingGit {
			nm.pendingGit = false
			cmd = tea.Batch(cmd, nm.gitEnter(nm.selected))
		}
		if sync := nm.gitSync(); sync != nil {
			cmd = tea.Batch(cmd, sync)
		}
	}
	return next, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.splash != nil {
		if cmd, handled := m.splashUpdate(msg); handled {
			return m, cmd
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.console != nil {
			_, _, cols, rows := consoleSize(m.width, m.height)
			m.console.cols, m.console.rows = cols, rows
			m.console.session.Resize(cols, rows)
			m.console.term.Resize(cols, rows)
		}
		return m, nil
	case tickMsg:
		m.tick++
		return m, tea.Batch(tickEvery(250*time.Millisecond), m.afterRefresh(), m.pendingChanged(), m.gitTick())
	case gitDiffMsg:
		m.gitOnDiff(msg)
		return m, nil
	case gitOpMsg:
		return m, tea.Batch(m.gitOnOp(msg), m.gitRefresh(false))
	case gitBatchMsg:
		return m, m.gitOnBatch(msg)
	case gitRefreshedMsg:
		return m, nil
	case lazygitDoneMsg:
		// Bubble Tea turns the mouse off to hand the terminal to lazygit and does not turn it back on
		cmds := []tea.Cmd{m.gitRefresh(true), tea.EnableMouseCellMotion}
		if msg.err != nil {
			cmds = append(cmds, m.notify("lazygit: "+msg.err.Error(), theme.Active().Red))
		}
		return m, tea.Batch(cmds...)
	case refreshMsg:
		return m, tea.Batch(m.afterRefresh(), m.pendingChanged())
	case stateMsg:
		return m, tea.Batch(m.stateChanged(msg.name), m.afterRefresh())
	case toastExpireMsg:
		if m.toast != nil && m.toast.id == msg.id {
			m.toast = nil
		}
		return m, nil
	case quitDoneMsg:
		m.quitting = true
		return m, tea.Quit
	case copyDoneMsg:
		text, ok := describeCopy(msg.res, msg.what, msg.unit, msg.n)
		p := theme.Active()
		if ok {
			return m, m.notify(text, p.Green)
		}
		return m, m.notify(text, p.Red)
	case exportDoneMsg:
		p := theme.Active()
		if msg.err != nil {
			return m, m.notify("export failed: "+msg.err.Error(), p.Red)
		}
		return m, m.notify("exported "+itoa(msg.lines)+" lines → "+msg.path+" (path copied)", p.Green)
	case openURLMsg:
		if !msg.ok {
			return m, m.notify("could not open "+msg.url+" (no browser opener found)", theme.Active().Red)
		}
		return m, nil
	case reloadDoneMsg:
		p := theme.Active()
		if msg.err != nil {
			return m, m.notify("orbit.yaml: "+msg.err.Error()+" (not applied)", p.Red)
		}
		restarted := 0
		for _, c := range msg.diff.Changed {
			if c.Restart {
				restarted++
			}
		}
		var parts []string
		if restarted > 0 {
			parts = append(parts, itoa(restarted)+" restarted")
		}
		if n := len(msg.diff.Added); n > 0 {
			parts = append(parts, itoa(n)+" added")
		}
		if n := len(msg.diff.Removed); n > 0 {
			parts = append(parts, itoa(n)+" removed")
		}
		text := "orbit.yaml applied"
		if len(parts) > 0 {
			text += ": " + strings.Join(parts, ", ")
		}
		return m, m.notify(text, p.Green)
	case historyMsg:
		if msg.name == m.selected && msg.rng == m.resRange {
			m.history, m.historyFor, m.historyRng = msg.buckets, msg.name, msg.rng
		}
		return m, nil
	case profilesLoadedMsg:
		return m, m.offerProfiles(msg)
	case taskDepsMsg:
		if !msg.ok {
			return m, m.notify("task "+msg.name+": its services did not become ready", theme.Active().Red)
		}
		if t := m.sup.Config().Tasks[msg.name]; t != nil {
			return m, m.openTask(t)
		}
		return m, nil
	case consoleExitMsg:
		if strings.HasPrefix(msg.name, "task:") {
			p := theme.Active()
			name := strings.TrimPrefix(msg.name, "task:")
			if m.console != nil && m.console.name == msg.name {
				m.console.exited, m.console.code, m.console.elapsed = true, msg.code, msg.elapsed
			} else if msg.code == 0 {
				return m, m.notify("✓ task "+name+" done", p.Green)
			} else {
				return m, m.notify("✖ task "+name+" exited ("+itoa(msg.code)+")", p.Red)
			}
			return m, nil
		}
		if m.console != nil && m.console.name == msg.name {
			m.closeConsole()
			if msg.code != 0 {
				return m, m.notify("console of "+msg.name+" exited (code "+itoa(msg.code)+")", theme.Active().Yellow)
			}
		}
		return m, nil
	case projectSwitchFailedMsg:
		m.pendingDir = ""
		m.mode = modeNormal
		return m, m.notify(msg.err.Error(), theme.Active().Red)
	case projectSwitchedMsg:
		return m.switched(msg)
	case pluginUpdateMsg:
		return m, m.onPluginUpdate(msg.u)
	case pluginDataMsg:
		return m, m.onPluginData(msg)
	case pluginSectionsMsg:
		m.onPluginSections(msg)
		return m, nil
	case pluginOptionsMsg:
		m.onPluginOptions(msg)
		return m, nil
	case pluginInvokedMsg:
		return m, m.onPluginInvoked(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

// handleKey dispatches a key press by the mode the interface is in.
func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// fast input (a paste, a script, a slow terminal) arrives as one message with several runes: each one is a key
	if k.Type == tea.KeyRunes && !k.Paste && len(k.Runes) > 1 && m.mode != modeConsole {
		var cmds []tea.Cmd
		for _, r := range k.Runes {
			if m.mode == modeStopping {
				break
			}
			cmds = append(cmds, m.keyInMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: k.Alt}))
		}
		return m, tea.Batch(cmds...)
	}
	return m, m.keyInMode(k)
}

func (m *Model) keyInMode(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	p := theme.Active()
	switch m.mode {
	case modeStopping:
		return nil
	case modeConsole:
		// everything goes to the program in the console; only the way out is ours
		if m.console == nil {
			m.mode = modeNormal
			return nil
		}
		if s == "ctrl+]" || m.console.exited {
			m.closeConsole()
			return nil
		}
		if k.Paste {
			m.console.session.Write([]byte("\x1b[200~" + string(k.Runes) + "\x1b[201~"))
			return nil
		}
		if b := keyBytes(k); len(b) > 0 {
			m.console.session.Write(b)
		}
		return nil
	case modePlugin:
		return m.keyInPluginDialog(k)
	case modeHelp:
		rows := len(m.helpRows())
		_, page := helpSize(rows, m.height)
		clampTo := func(v int) int { return max(0, min(v, max(0, rows-page))) }
		switch s {
		case "down", "j":
			m.helpScroll = clampTo(m.helpScroll + 1)
		case "up", "k":
			m.helpScroll = clampTo(m.helpScroll - 1)
		case "pgdown", "ctrl+d":
			m.helpScroll = clampTo(m.helpScroll + page)
		case "pgup", "ctrl+u":
			m.helpScroll = clampTo(m.helpScroll - page)
		case "g", "home":
			m.helpScroll = 0
		case "G", "end":
			m.helpScroll = clampTo(rows)
		default:
			m.mode = modeNormal
		}
		return nil
	case modeEnv:
		l, _ := m.envLayoutFor(m.width)
		entries, _ := m.envEntries()
		page := envPageSize(m.height)
		move := func(to int) {
			m.envCursor = max(0, min(to, len(entries)-1))
			m.envScroll = envScrollTo(l, m.envCursor, m.envScroll, page)
		}
		switch s {
		case "esc", "q":
			if m.envAnchor >= 0 {
				m.envAnchor = -1
			} else {
				m.mode = modeNormal
			}
		case "e":
			m.mode = modeNormal
		case "r":
			m.envReveal = !m.envReveal
		case "v", " ":
			if m.envAnchor >= 0 {
				m.envAnchor = -1
			} else {
				m.envAnchor = m.envCursor
			}
		case "y", "enter":
			return m.envCopy(false)
		case "Y":
			return m.envCopy(true)
		case "down", "j":
			move(m.envCursor + 1)
		case "up", "k":
			move(m.envCursor - 1)
		case "pgdown", "ctrl+d":
			move(m.envCursor + max(1, page/2))
		case "pgup", "ctrl+u":
			move(m.envCursor - max(1, page/2))
		case "g", "home":
			move(0)
		case "G", "end":
			move(len(entries) - 1)
		}
		return nil
	case modeQuit:
		switch s {
		case "y", "Y", "s", "S", "enter":
			return m.doQuit("stop")
		case "d", "D":
			return m.doQuit("detach")
		case "n", "esc", "q":
			m.mode = modeNormal
		}
		return nil
	case modeProjects:
		return m.keyInProjects(k)
	case modeSwitch:
		return m.keyInSwitch(k)
	case modeReload:
		switch s {
		case "y", "Y", "enter":
			return m.applyReload()
		case "n", "esc", "q":
			m.reload = nil
			m.mode = modeNormal
		}
		return nil
	case modePalette:
		matches := filterCommands(m.commands(), m.palette.value())
		switch s {
		case "esc":
			m.mode = modeNormal
		case "up", "ctrl+p":
			m.paletteIdx = max(0, m.paletteIdx-1)
		case "down", "ctrl+n":
			m.paletteIdx = min(max(0, len(matches)-1), m.paletteIdx+1)
		case "enter":
			m.mode = modeNormal
			if m.paletteIdx < len(matches) {
				return matches[m.paletteIdx].run(m)
			}
		default:
			before := m.palette.value()
			m.palette.handle(k)
			if m.palette.value() != before {
				m.paletteIdx = 0
			}
		}
		return nil
	case modeTasks:
		return m.keyInTasks(k)
	case modeProfiles:
		return m.keyInProfiles(k)
	case modeTheme:
		switch s {
		case "esc", "q":
			m.cancelTheme()
		case "enter":
			return m.saveTheme()
		case "up", "k", "ctrl+p":
			m.previewTheme(m.themeIdx - 1)
		case "down", "j", "ctrl+n":
			m.previewTheme(m.themeIdx + 1)
		}
		return nil
	case modeFilter:
		switch s {
		case "esc":
			m.clearLogQueries()
			m.mode = modeNormal
		case "enter":
			m.mode = modeNormal
			// a new search starts at its newest match
			if m.barKind == "search" && m.search != "" {
				return m.jumpMatch(1, true)
			}
		case "tab":
			// the text typed so far moves over to the other kind of bar
			text := m.bar.value()
			if m.barKind == "filter" {
				m.barKind = "search"
				m.filter, m.search = "", text
			} else {
				m.barKind = "filter"
				m.filter, m.search = text, ""
			}
			m.match = 0
		default:
			m.bar.handle(k)
			if m.barKind == "filter" {
				m.filter = m.bar.value()
			} else {
				m.search = m.bar.value()
			}
			m.scrollBack = 0
		}
		return nil
	case modeCopy:
		return m.keyInCopy(k)
	}

	// ---- normal mode
	if m.plug.filtering != nil {
		return m.pluginFilterKeys(k)
	}
	if a := m.pluginActionForKey(s); a != nil {
		return m.pluginRun(a, m.selected)
	}
	// a text prompt, a question or the diff's copy mode inside the Git view takes every key, except the way out
	if m.view == "git" {
		g := m.gs()
		if g.selOn && m.focus != paneGitDiff {
			g.selOn, g.selAnchor = false, -1 // the focus moved away (a click): the cursor goes with it
		}
		if g.prompt != nil || g.confirm != nil || g.selOn {
			if s == "ctrl+c" {
				return m.requestQuit()
			}
			cmd, _ := m.gitKeys(k)
			return cmd
		}
	}
	// `p` is pull in the Git view
	if s == "p" && m.view != "git" {
		return m.openProfiles()
	}
	switch s {
	case "ctrl+c", "q":
		return m.requestQuit()
	case "ctrl+p", ":":
		m.palette = input{}
		m.paletteIdx = 0
		m.mode = modePalette
		return nil
	case "?":
		m.mode, m.helpScroll = modeHelp, 0
		return nil
	case "T":
		m.openThemePicker()
		return nil
	case "!":
		return m.openTasks()
	case "U":
		return m.openReload()
	case "P":
		m.openProjects()
		return nil
	case "tab", "shift+tab":
		panes := m.viewDef().panes
		i := 0
		for j, pn := range panes {
			if pn == m.focus {
				i = j
			}
		}
		step := 1
		if s == "shift+tab" {
			step = len(panes) - 1
		}
		m.focus = panes[(i+step)%len(panes)]
		return nil
	case "z":
		m.zoomed = !m.zoomed
		return nil
	case "+":
		m.resize(1)
		return nil
	case "-":
		m.resize(-1)
		return nil
	case "=":
		m.resetSize()
		return nil
	case "esc":
		if pn := m.focusedPluginPane(); pn != nil && pn.filter != "" {
			pn.filter = ""
			if pn.refresh() {
				return m.pluginSelectionChanged(pn)
			}
			return nil
		}
		if m.view == "git" {
			if cmd, handled := m.gitKeys(k); handled {
				return cmd
			}
		}
		if m.search != "" || m.filter != "" {
			m.clearLogQueries()
			return nil
		}
		if m.zoomed {
			m.zoomed = false
		}
		return nil
	}
	if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		if vs := m.allViews(); int(s[0]-'1') < len(vs) {
			m.setView(vs[int(s[0]-'1')].id)
			return m.loadHistory()
		}
	}
	if m.view == "git" {
		// the view's own keys; the service shortcuts do not apply in a view with no service list
		cmd, _ := m.gitKeys(k)
		return cmd
	}
	if vd := m.viewDef(); vd.plugin != nil {
		if cmd, handled := m.pluginPaneKeys(k); handled {
			return cmd
		}
		if m.focus != paneServices {
			return nil
		}
	}
	if s == "h" && m.view == "dashboard" {
		return m.cycleRange()
	}

	page := m.pageSize()
	if m.focus == paneLogs {
		switch s {
		case "down", "j":
			m.scrollBy(-1)
			return nil
		case "up", "k":
			m.scrollBy(1)
			return nil
		case "ctrl+d":
			m.scrollBy(-(page / 2))
			return nil
		case "ctrl+u":
			m.scrollBy(page / 2)
			return nil
		case "g", "home":
			m.scrollBack = 1 << 30
			m.clampScroll()
			return nil
		case "G", "end":
			m.scrollBack = 0
			return nil
		case "v":
			lines := m.viewLines()
			if len(lines) == 0 {
				return m.notify("no logs to select", p.Yellow)
			}
			at := max(0, len(lines)-1-m.scrollBack)
			m.copyCursor, m.copyAnchor = lines[min(at, len(lines)-1)].Seq, 0
			m.mode = modeCopy
			return nil
		}
	}

	if m.view == "graph" && m.focus == paneGraph {
		if dir := map[string]string{"up": "up", "down": "down", "left": "left", "right": "right"}[s]; dir != "" {
			if n := neighbourInDirection(m.graphLayout(), m.selected, dir); n != "" {
				m.setSelected(n)
			}
			return m.loadHistory()
		}
	}
	names := m.names()
	switch s {
	case "down", "j":
		m.moveSelection(1)
		return m.loadHistory()
	case "up", "k":
		m.moveSelection(-1)
		return m.loadHistory()
	case "g", "home":
		if len(names) > 0 {
			m.setSelected(names[0])
		}
		return m.loadHistory()
	case "G", "end":
		if len(names) > 0 {
			m.setSelected(names[len(names)-1])
		}
		return m.loadHistory()
	}

	sup := m.sup
	sel := m.selected
	if sel == "" {
		return nil
	}
	switch s {
	case " ":
		return m.op(func() { sup.Toggle(sel) })
	case "s":
		return m.op(func() { sup.Start(sel) })
	case "x":
		return m.op(func() { sup.Stop(sel, supervisor.StopOptions{}) })
	case "r":
		return m.op(func() { sup.Restart(sel) })
	case "S":
		return m.op(sup.StartAll)
	case "X":
		return m.op(sup.StopAll)
	case "W":
		return m.toggleWatch(sel)
	case "R":
		return m.restartAll()
	case "e":
		m.openEnv()
		return nil
	case "i":
		return m.openConsole(sel)
	case "o":
		return m.openService(sel)
	case "L":
		return m.serviceLazygit(sel)
	case "enter", "l":
		m.logScope = "selected"
		m.scrollBack = 0
		m.focus = paneLogs
		m.setView("logs")
		m.focus = paneLogs
		return nil
	case "a":
		if m.logScope == "all" {
			m.logScope = "selected"
		} else {
			m.logScope = "all"
		}
		m.scrollBack = 0
		return nil
	case "/":
		m.barKind = "filter"
		m.bar = input{}
		if m.filter != "" {
			m.bar.set(m.filter)
		}
		m.mode = modeFilter
		return nil
	case "f":
		m.scrollBack = 0
		return nil
	case "t":
		m.showTime = !m.showTime
		return nil
	case "w":
		m.wrap = !m.wrap
		return nil
	case "b":
		m.fold = !m.fold
		return nil
	case "Y":
		return m.copyLines(m.rawViewLines(), "all logs in view")
	case "E":
		return m.exportLogs()
	case "n":
		return m.jumpMatch(1, false)
	case "N":
		return m.jumpMatch(-1, false)
	case "c":
		target := sel
		if m.view == "logs" && m.logScope == "all" {
			target = ""
		}
		sup.ClearLogs(target)
		return m.notify("logs cleared", p.Muted)
	case "pgup":
		m.scrollBy(page)
		return nil
	case "pgdown":
		m.scrollBy(-page)
		return nil
	}
	return nil
}

func (m *Model) keyInCopy(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	p := theme.Active()
	lines := m.viewLines()
	if len(lines) == 0 || m.copyCursor == 0 {
		m.exitCopy()
		return nil
	}
	at := 0
	for i, l := range lines {
		if l.Seq == m.copyCursor {
			at = i
		}
	}
	page := m.pageSize()
	half := max(2, page/2)
	go2 := func(i int) {
		i = max(0, min(len(lines)-1, i))
		m.copyCursor = lines[i].Seq
		m.revealSeq(m.copyCursor)
	}
	switch s {
	case "esc", "q":
		m.exitCopy()
		return nil
	case "down", "j":
		go2(at + 1)
		return nil
	case "up", "k":
		go2(at - 1)
		return nil
	case "ctrl+d":
		go2(at + half)
		return nil
	case "ctrl+u":
		go2(at - half)
		return nil
	case "pgdown":
		go2(at + page)
		return nil
	case "pgup":
		go2(at - page)
		return nil
	case "g", "home":
		go2(0)
		return nil
	case "G", "end":
		go2(len(lines) - 1)
		return nil
	}
	// on a stack trace row, enter / space / o open or close it (everywhere else they copy / select)
	cur := lines[at]
	head := 0
	if m.fold {
		switch {
		case cur.TraceOf != 0:
			head = cur.TraceOf
		case cur.Trace != nil && cur.Trace.Frames >= 2:
			head = cur.Seq
		}
	}
	if head != 0 && (s == "o" || s == " " || s == "enter") {
		m.toggleTrace(head)
		if cur.TraceOf != 0 {
			m.copyCursor = head
		}
		return nil
	}
	if s == "o" {
		if m.fold {
			return m.notify("not in a stack trace", p.Yellow)
		}
		return m.notify("stack trace folding is off (b)", p.Yellow)
	}
	switch s {
	case "v", " ":
		if m.copyAnchor == 0 {
			m.copyAnchor = m.copyCursor
		} else {
			m.copyAnchor = 0
		}
		return nil
	case "y", "enter":
		from := at
		if m.copyAnchor != 0 {
			for i, l := range lines {
				if l.Seq == m.copyAnchor {
					from = i
				}
			}
		}
		rng := lines[min(at, from) : max(at, from)+1]
		all := m.rawViewLines()
		m.exitCopy()
		return m.copyLines(logs.WithFrames(rng, all), "selection")
	}
	return nil
}

// handleMouse: clicks select services, focus panels and switch views; the wheel scrolls.
func (m *Model) handleMouse(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode == modeConsole || m.mode == modeStopping {
		return m, nil
	}
	m.geo.mu.Lock()
	g := m.geo.geoData
	m.geo.mu.Unlock()
	inLog := g.logH > 0 && e.X >= g.logX && e.X < g.logX+g.logW && e.Y >= g.logY && e.Y < g.logY+g.logH
	inList := e.X >= g.listX && e.X < g.listX+g.listW && e.Y >= g.listY && e.Y < g.listY+g.listH
	switch e.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if m.mode != modeNormal && m.mode != modeCopy {
			return m, nil
		}
		delta := 3
		if e.Button == tea.MouseButtonWheelDown {
			delta = -3
		}
		switch {
		case inLog:
			m.scrollBack = max(0, min(g.maxBack, m.scrollBack+delta))
		case inList:
			m.moveSelection(-delta / 3)
			return m, m.loadHistory()
		default:
			if r, ok := regionAt(g.hits, e.X, e.Y); ok && m.mode == modeNormal {
				return m, m.wheelRegion(r, delta)
			}
		}
	case tea.MouseButtonLeft:
		if m.mode == modeCopy && inLog {
			return m, m.copyDrag(e, g)
		}
		if e.Action != tea.MouseActionPress || m.mode != modeNormal {
			return m, nil
		}
		if e.Y == 0 {
			for _, b := range g.badges {
				if e.X >= b.x0 && e.X < b.x1 {
					for i := range m.plug.actions {
						if a := &m.plug.actions[i]; a.plugin == b.plugin && a.def.ID == b.action {
							return m, m.pluginRun(a, m.selected)
						}
					}
					return m, nil
				}
			}
			for _, t := range g.tabs {
				if e.X >= t.x0 && e.X < t.x1 {
					m.setView(t.view)
					return m, m.loadHistory()
				}
			}
			return m, nil
		}
		switch {
		case inList:
			m.focus = paneServices
			row := e.Y - g.listY - 1
			names := m.sup.Order()
			if row >= 0 && g.listFirst+row < len(names) {
				m.setSelected(names[g.listFirst+row])
				return m, m.loadHistory()
			}
		case inLog:
			m.focus = paneLogs
			row := e.Y - g.logY - 1
			if row >= 0 && row < len(g.logRows) {
				seq := g.logRows[row]
				for _, l := range m.sup.LogStore().Lines(m.logService()) {
					if l.Seq == seq && m.fold && l.Trace != nil && l.Trace.Frames >= 2 {
						m.toggleTrace(seq)
					}
				}
			}
		case g.graphW > 0 && e.X >= g.graphX && e.X < g.graphX+g.graphW && e.Y >= g.graphY && e.Y < g.graphY+g.graphH:
			m.focus = paneGraph
			gx := e.X - g.graphX - 1 - g.graphPadX + g.graphOffX
			gy := e.Y - g.graphY - 1 - g.graphPadY + g.graphOffY
			for _, b := range m.graphLayout().Boxes {
				if gx >= b.X && gx < b.X+b.W && gy >= b.Y && gy < b.Y+b.H {
					m.setSelected(b.Name)
					return m, m.loadHistory()
				}
			}
		default:
			if r, ok := regionAt(g.hits, e.X, e.Y); ok {
				return m, m.clickRegion(r, e.Y)
			}
		}
	}
	return m, nil
}

func regionAt(hits []hitRegion, x, y int) (hitRegion, bool) {
	for _, r := range hits {
		if x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h {
			return r, true
		}
	}
	return hitRegion{}, false
}

// clickRegion focuses the panel under the mouse and, in a list, selects the row that was clicked.
func (m *Model) clickRegion(r hitRegion, y int) tea.Cmd {
	if strings.HasPrefix(string(r.pane), pluginViewPrefix) {
		return m.pluginClick(r, y)
	}
	m.focus = r.pane
	if r.items == 0 {
		return nil
	}
	row := y - r.y - 1
	idx := r.first + row
	if row < 0 || idx >= r.items {
		return nil
	}
	g := m.gs()
	switch {
	case r.pane == paneRepos:
		g.index = idx
		g.sel = map[pane]int{}
	case r.pane == paneGitLog:
		g.logSel = idx
	case isGitList(r.pane):
		g.sel[r.pane] = idx
		g.lastList = r.pane
	}
	return m.gitSync()
}

// wheelRegion scrolls the panel under the mouse: lists move their selection, the diff scrolls.
func (m *Model) wheelRegion(r hitRegion, delta int) tea.Cmd {
	if strings.HasPrefix(string(r.pane), pluginViewPrefix) {
		return m.pluginWheel(r, delta)
	}
	g := m.gs()
	step := -delta / 3
	switch {
	case r.pane == paneGitDiff:
		g.scroll = max(0, g.scroll+step*3)
		return nil
	case r.items == 0:
		return nil
	case r.pane == paneRepos:
		g.index = max(0, min(r.items-1, g.index+step))
		g.sel = map[pane]int{}
	case r.pane == paneGitLog:
		v, ok := m.gitView()
		if !ok {
			return nil
		}
		at := max(0, min(r.items-1, v.logAt+step))
		g.logSel = at
		if at == r.items-1 {
			g.logSel = -1
		}
	case isGitList(r.pane):
		g.sel[r.pane] = max(0, min(r.items-1, g.at(r.pane, r.items)+step))
		g.lastList = r.pane
	}
	return m.gitSync()
}

// copyDrag selects log lines with the mouse in copy mode: a press sets the cursor, dragging extends from there.
func (m *Model) copyDrag(e tea.MouseMsg, g geoData) tea.Cmd {
	row := e.Y - g.logY - 1
	if row < 0 || row >= len(g.logRows) {
		return nil
	}
	seq := g.logRows[row]
	switch e.Action {
	case tea.MouseActionPress:
		m.copyCursor, m.copyAnchor = seq, 0
	case tea.MouseActionMotion:
		if m.copyAnchor == 0 {
			m.copyAnchor = m.copyCursor
		}
		m.copyCursor = seq
	}
	return nil
}

// switched replaces the model with a fresh one for the new project: every piece of per-project interface state
// (selection, filters, scroll) starts over, as if orbit had been opened there.
func (m *Model) switched(msg projectSwitchedMsg) (tea.Model, tea.Cmd) {
	m.consoles.CloseAll()
	if m.console != nil {
		m.closeConsole()
	}
	opts := m.opts
	opts.Sup = msg.sup
	opts.Relaunch = msg.relaunch
	opts.StartWithPicker = false
	opts.ThemeErrors = nil
	opts.InitialTheme = m.themeName
	if opts.Rebind != nil {
		opts.Rebind(msg.sup)
	}
	next := New(opts)
	next.width, next.height = m.width, m.height
	next.tick = m.tick
	var cmds []tea.Cmd
	if msg.relaunch != nil {
		next.reload = &reloadOffer{diff: msg.relaunch.Diff, cfg: msg.relaunch.Config}
		next.mode = modeReload
	}
	cmds = append(cmds, next.notify("opened "+msg.sup.Config().Name, theme.Active().Accent))
	return next, tea.Batch(cmds...)
}
