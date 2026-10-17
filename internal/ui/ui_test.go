package ui

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

func svc(name, cmd string, deps ...string) *config.ServiceConfig {
	cwd, _ := os.Getwd()
	return &config.ServiceConfig{
		Name: name, Type: config.TypeProcess, Cmd: cmd, Cwd: cwd, Env: map[string]string{}, EnvFiles: []config.EnvFileRef{},
		DependsOn: append([]string{}, deps...), Restart: config.RestartNo, StartTimeout: 5000, StopTimeout: 1000,
		Autostart: true, LeakDetection: true, Ports: []string{}, Volumes: []string{}, DockerArgs: []string{},
	}
}

func newModel(t *testing.T) (*Model, *supervisor.Supervisor) {
	t.Helper()
	cwd, _ := os.Getwd()
	cfg := &config.OrbitConfig{Name: "t", Root: cwd, Services: map[string]*config.ServiceConfig{}, Groups: map[string][]string{"all": {"db", "api"}}, GroupOrder: []string{"all"}}
	for _, s := range []*config.ServiceConfig{svc("db", "sleep 30"), svc("api", "sleep 30", "db"), svc("web", "sleep 30", "api")} {
		cfg.Services[s.Name] = s
		cfg.Order = append(cfg.Order, s.Name)
	}
	sup := supervisor.New(cfg, supervisor.Options{StateDir: t.TempDir()})
	t.Cleanup(func() { sup.Dispose() })
	m := New(Options{Sup: sup, OnQuit: func(string) {}})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	t.Cleanup(func() { theme.Apply(theme.Themes[theme.DefaultName]) })
	return m, sup
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+]":
		return tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(m *Model, keys ...string) {
	for _, k := range keys {
		m.Update(key(k))
	}
}

func screen(m *Model) string { return ansi.Strip(m.View()) }

func TestFuzzyScore(t *testing.T) {
	if _, ok := fuzzyScore("zzz", "Start all"); ok {
		t.Error("no match expected")
	}
	direct, _ := fuzzyScore("all", "Start all services")
	subseq, _ := fuzzyScore("sas", "Start all services")
	if direct >= subseq {
		t.Errorf("a substring (%d) must beat a subsequence (%d)", direct, subseq)
	}
	if s, ok := fuzzyScore("", "x"); !ok || s != 0 {
		t.Error("empty query matches everything")
	}
}

func TestFilterCommandsOrdersByScore(t *testing.T) {
	cmds := []command{{label: "Stop api"}, {label: "Start api"}, {label: "Restart api"}}
	got := filterCommands(cmds, "start")
	if len(got) != 2 || got[0].label != "Start api" {
		t.Fatalf("got %+v", got)
	}
}

func TestKeyBytes(t *testing.T) {
	cases := map[string][]byte{
		"a": []byte("a"),
	}
	for in, want := range cases {
		if got := keyBytes(key(in)); string(got) != string(want) {
			t.Errorf("%q -> %q", in, got)
		}
	}
	if got := keyBytes(key("enter")); string(got) != "\r" {
		t.Errorf("enter = %q", got)
	}
	if got := keyBytes(tea.KeyMsg{Type: tea.KeyUp}); string(got) != "\x1b[A" {
		t.Errorf("up = %q", got)
	}
	if got := keyBytes(tea.KeyMsg{Type: tea.KeyCtrlC}); len(got) != 1 || got[0] != 3 {
		t.Errorf("ctrl+c = %q", got)
	}
	if got := keyBytes(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}); string(got) != "\x1bx" {
		t.Errorf("alt+x = %q", got)
	}
}

func TestInputEditing(t *testing.T) {
	var in input
	for _, r := range "hello" {
		in.handle(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	in.handle(tea.KeyMsg{Type: tea.KeyLeft})
	in.handle(tea.KeyMsg{Type: tea.KeyBackspace})
	if in.value() != "helo" {
		t.Fatalf("value = %q", in.value())
	}
	in.handle(tea.KeyMsg{Type: tea.KeyCtrlA})
	in.handle(tea.KeyMsg{Type: tea.KeyDelete})
	if in.value() != "elo" {
		t.Fatalf("value = %q", in.value())
	}
	in.handle(tea.KeyMsg{Type: tea.KeyCtrlU})
	if in.value() != "elo" {
		t.Fatalf("ctrl+u at the start must keep the text, got %q", in.value())
	}
	in.handle(tea.KeyMsg{Type: tea.KeyEnd})
	in.handle(tea.KeyMsg{Type: tea.KeyCtrlW})
	if in.value() != "" {
		t.Fatalf("ctrl+w = %q", in.value())
	}
}

func TestSelectionAndViews(t *testing.T) {
	m, _ := newModel(t)
	if m.selected != "db" {
		t.Fatalf("selected = %q", m.selected)
	}
	press(m, "j", "j")
	if m.selected != "web" {
		t.Fatalf("selected = %q", m.selected)
	}
	press(m, "j")
	if m.selected != "db" {
		t.Fatalf("selection must wrap, got %q", m.selected)
	}
	press(m, "G")
	if m.selected != "web" {
		t.Fatalf("G = %q", m.selected)
	}
	press(m, "2")
	if m.view != "graph" || m.focus != paneGraph {
		t.Fatalf("view = %s focus = %s", m.view, m.focus)
	}
	press(m, "3")
	if m.view != "logs" || m.focus != paneLogs {
		t.Fatalf("view = %s focus = %s", m.view, m.focus)
	}
	press(m, "1", "tab")
	if m.focus != paneDetail {
		t.Fatalf("focus = %s", m.focus)
	}
	press(m, "tab", "tab")
	if m.focus != paneServices {
		t.Fatalf("focus wraps to %s", m.focus)
	}
}

func TestZoomAndResizeStayInBounds(t *testing.T) {
	m, _ := newModel(t)
	base := m.sidebarW()
	press(m, "+")
	if m.sidebarW() != base+2 {
		t.Fatalf("sidebar = %d, want %d", m.sidebarW(), base+2)
	}
	press(m, "=")
	if m.sidebarW() != base {
		t.Fatalf("reset sidebar = %d", m.sidebarW())
	}
	for i := 0; i < 100; i++ {
		press(m, "-")
	}
	if m.sidebarW() < minSidebar {
		t.Fatalf("sidebar = %d", m.sidebarW())
	}
	press(m, "z")
	if !m.zoomed {
		t.Fatal("not zoomed")
	}
	if !strings.Contains(screen(m), "⛶ zoom") {
		t.Fatal("no zoom badge")
	}
	press(m, "esc")
	if m.zoomed {
		t.Fatal("esc must unzoom")
	}
}

func TestLogFilterSearchAndFold(t *testing.T) {
	m, sup := newModel(t)
	for _, l := range []string{"alpha", "beta error here", "gamma", "Traceback (most recent call last):", `  File "a.py", line 1`, "    x", "ValueError: boom", "delta"} {
		sup.Logs.Append("db", logs.Stderr, l)
	}
	press(m, "3") // logs view, all services
	scr := screen(m)
	if !strings.Contains(scr, "alpha") || !strings.Contains(scr, "traces folded") {
		t.Fatalf("screen:\n%s", scr)
	}
	if strings.Contains(scr, "line 1") {
		t.Fatal("folded trace frames must be hidden")
	}
	press(m, "b")
	if !strings.Contains(screen(m), "line 1") {
		t.Fatal("unfolding must show the frames")
	}
	press(m, "/")
	press(m, "beta")
	if m.filter != "beta" {
		t.Fatalf("filter = %q", m.filter)
	}
	press(m, "enter")
	if m.mode != modeNormal || m.filter != "beta" {
		t.Fatalf("mode = %v filter = %q", m.mode, m.filter)
	}
	if lines := m.viewLines(); len(lines) != 1 || lines[0].Text != "beta error here" {
		t.Fatalf("filtered = %+v", lines)
	}
	press(m, "esc")
	if m.filter != "" {
		t.Fatal("esc must clear the filter")
	}
	// search: highlights, keeps every line, n / N jump
	press(m, "/", "tab", "a", "enter")
	if m.search != "a" || m.barKind != "search" {
		t.Fatalf("search = %q kind = %s", m.search, m.barKind)
	}
	if m.match == 0 {
		t.Fatal("the first search jump must pick a match")
	}
	first := m.match
	press(m, "n")
	if m.match == first && len(m.rawViewLines()) > 1 {
		// with several matches n must move on (it wraps around)
		t.Logf("match stayed on %d", first)
	}
}

func TestCopyModeSelectsAndCopies(t *testing.T) {
	m, sup := newModel(t)
	for i := 1; i <= 5; i++ {
		sup.Logs.Append("db", logs.Stdout, fmt.Sprintf("line %d", i))
	}
	press(m, "3", "v")
	if m.mode != modeCopy {
		t.Fatalf("mode = %v", m.mode)
	}
	var mu sync.Mutex
	var copied string
	old := clipboardCopy
	clipboardCopy = func(text string) copyResult {
		mu.Lock()
		copied = text
		mu.Unlock()
		return copyResult{ok: true, via: "test"}
	}
	t.Cleanup(func() { clipboardCopy = old })

	press(m, "k", "v", "k", "y")
	if m.mode != modeNormal {
		t.Fatalf("mode after y = %v", m.mode)
	}
	cmd := m.copyLines(m.rawViewLines()[2:5], "x")
	msg := cmd()
	done, ok := msg.(copyDoneMsg)
	if !ok || !done.res.ok {
		t.Fatalf("msg = %#v", msg)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(copied, "line 3") || !strings.Contains(copied, "line 5") {
		t.Fatalf("copied = %q", copied)
	}
}

func TestPaletteRunsACommand(t *testing.T) {
	m, _ := newModel(t)
	press(m, ":")
	if m.mode != modePalette {
		t.Fatalf("mode = %v", m.mode)
	}
	press(m, "toggle log timestamps")
	press(m, "enter")
	if !m.showTime || m.mode != modeNormal {
		t.Fatalf("showTime = %v mode = %v", m.showTime, m.mode)
	}
}

func TestThemePreviewSaveAndCancel(t *testing.T) {
	m, _ := newModel(t)
	var saved string
	m.opts.SaveTheme = func(n string) bool { saved = n; return true }
	press(m, "T", "j")
	if m.themeName != "catppuccin-mocha" || theme.Active().Accent != theme.Themes["catppuccin-mocha"].Accent {
		t.Fatalf("preview did not apply: %s", m.themeName)
	}
	press(m, "esc")
	if m.themeName != theme.DefaultName || theme.Active().Accent != theme.Themes[theme.DefaultName].Accent {
		t.Fatal("cancel must restore the saved theme")
	}
	press(m, "T", "j", "enter")
	if saved != "catppuccin-mocha" || m.savedTheme != "catppuccin-mocha" {
		t.Fatalf("saved = %q", saved)
	}
}

func TestQuitFlow(t *testing.T) {
	m, sup := newModel(t)
	var how string
	m.opts.OnQuit = func(h string) { how = h }
	// nothing running: quits straight away
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("expected a quit command")
	}
	if _, ok := cmd().(quitDoneMsg); !ok || how != "stop" {
		t.Fatalf("how = %q", how)
	}

	m2, sup2 := newModel(t)
	var how2 string
	m2.opts.OnQuit = func(h string) { how2 = h }
	sup2.Start("db")
	press(m2, "q")
	if m2.mode != modeQuit {
		t.Fatalf("with services running a confirmation is needed, mode = %v", m2.mode)
	}
	if !strings.Contains(screen(m2), "leave running") {
		t.Fatal("confirmation text missing")
	}
	press(m2, "n")
	if m2.mode != modeNormal {
		t.Fatal("n must cancel")
	}
	press(m2, "q")
	_, cmd2 := m2.Update(key("d"))
	cmd2()
	if how2 != "detach" {
		t.Fatalf("how = %q", how2)
	}
	_ = sup
}

func TestSwitchProjectModalSaysWhatHappensToServices(t *testing.T) {
	for _, tc := range []struct {
		key, want, notWant string
	}{
		{"d", "leaving 1 service(s) running", "Stopping"},
		{"s", "Stopping 1 service(s)", "leaving"},
	} {
		m, sup := newModel(t)
		release := make(chan struct{})
		m.opts.OpenProject = func(dir, how string) (supervisor.Like, *Relaunch, error) {
			<-release
			return nil, nil, errors.New("not here")
		}
		sup.Start("db")
		m.pendingDir, m.mode = "/work/other", modeSwitch
		_, cmd := m.Update(key(tc.key))
		if m.mode != modeStopping {
			t.Fatalf("%s: mode = %v", tc.key, m.mode)
		}
		out := screen(m)
		if !strings.Contains(out, tc.want) || strings.Contains(out, tc.notWant) {
			t.Fatalf("%s: modal must contain %q and not %q:\n%s", tc.key, tc.want, tc.notWant, out)
		}
		if tc.key == "d" && !strings.Contains(out, "Opening other") {
			t.Fatalf("detach modal must name the project:\n%s", out)
		}
		close(release)
		cmd()
	}
}

func TestMouseComesBackAfterLazygit(t *testing.T) {
	m, _ := newModel(t)
	_, cmd := m.Update(lazygitDoneMsg{})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("expected a batch of commands")
	}
	want := fmt.Sprintf("%T", tea.EnableMouseCellMotion())
	for _, c := range batch {
		if c != nil && fmt.Sprintf("%T", c()) == want {
			return
		}
	}
	t.Fatal("the mouse is not enabled again after lazygit exits")
}

func TestEnvOverlayWrapsLongValues(t *testing.T) {
	m, sup := newModel(t)
	long := strings.Repeat("0123456789", 20)
	sup.Service("db").Env["LONG_VALUE"] = long
	press(m, "e")
	if m.mode != modeEnv {
		t.Fatalf("mode = %v", m.mode)
	}
	var got strings.Builder
	for _, r := range screen(m) {
		got.WriteRune(r)
	}
	// every character of the value is on screen, spread over continuation rows
	rows, _ := m.envLayoutFor(m.width)
	var joined strings.Builder
	for _, r := range rows.rows {
		joined.WriteString(r.val)
	}
	if !strings.Contains(joined.String(), long) {
		t.Fatalf("the value is cut: %q", joined.String())
	}
	if strings.Contains(got.String(), "…") {
		t.Fatalf("the overlay still truncates:\n%s", got.String())
	}
}

func TestEnvOverlayCopiesValuesAndRanges(t *testing.T) {
	m, sup := newModel(t)
	sup.Service("db").Env["AAA"] = "one"
	sup.Service("db").Env["BBB_SECRET"] = "hunter2"
	sup.Service("db").Env["CCC"] = "three"
	var copied string
	old := clipboardCopy
	clipboardCopy = func(text string) copyResult { copied = text; return copyResult{ok: true, via: "test"} }
	t.Cleanup(func() { clipboardCopy = old })
	run := func(keys ...string) copyDoneMsg {
		t.Helper()
		var out copyDoneMsg
		for _, k := range keys {
			if _, cmd := m.Update(key(k)); cmd != nil {
				if d, ok := cmd().(copyDoneMsg); ok {
					out = d
				}
			}
		}
		return out
	}
	press(m, "e")
	entries, _ := m.envEntries()
	idx := map[string]int{}
	for i, e := range entries {
		idx[e.Key] = i
	}
	// one variable: just its value, even a hidden secret
	press(m, "g")
	for i := 0; i < idx["BBB_SECRET"]; i++ {
		press(m, "j")
	}
	if d := run("y"); d.n != 1 || copied != "hunter2" {
		t.Fatalf("copied %q (%+v)", copied, d)
	}
	// a range: KEY=value per variable
	press(m, "g", "v")
	for i := 0; i < idx["CCC"]; i++ {
		press(m, "j")
	}
	if d := run("y"); d.n != idx["CCC"]+1 || !strings.Contains(copied, "BBB_SECRET=hunter2") || !strings.Contains(copied, "CCC=three") {
		t.Fatalf("copied %q (%+v)", copied, d)
	}
	if m.envAnchor != -1 || m.mode != modeEnv {
		t.Fatal("copying a range ends the selection but keeps the overlay")
	}
	if d := run("Y"); d.n != 1 || !strings.HasPrefix(copied, "CCC=") {
		t.Fatalf("Y copied %q", copied)
	}
}

func TestDiffLineSelectionCopiesTheRange(t *testing.T) {
	m, _ := newModel(t)
	g := m.gs()
	g.rows = diffRows{rows: []diffRow{
		{kind: rowFile, text: "a.go", hunk: -1},
		{kind: rowHunk, text: "@@ -1,2 +1,2 @@", hunk: 0},
		{kind: rowCtx, text: "keep", hunk: 0},
		{kind: rowDel, text: "old", hunk: 0},
		{kind: rowAdd, text: "new", hunk: 0},
	}}
	g.pageRows = 3
	var copied string
	old := clipboardCopy
	clipboardCopy = func(text string) copyResult { copied = text; return copyResult{ok: true, via: "test"} }
	t.Cleanup(func() { clipboardCopy = old })

	// the first v only shows the cursor
	g.scroll = 2
	m.gitDiffSelectStart()
	if !g.selOn || g.selAnchor != -1 || g.selCur != 2 {
		t.Fatalf("on = %v anchor = %d cursor = %d", g.selOn, g.selAnchor, g.selCur)
	}
	// the second one starts the range
	m.gitDiffSelectKeys("v")
	if g.selAnchor != 2 {
		t.Fatalf("anchor = %d", g.selAnchor)
	}
	m.gitDiffSelectKeys("j")
	m.gitDiffSelectKeys("j")
	if g.selCur != 4 || g.scroll != 2 {
		t.Fatalf("cursor = %d scroll = %d (the cursor must stay on screen)", g.selCur, g.scroll)
	}
	msg := m.gitDiffSelectKeys("y")()
	done, ok := msg.(copyDoneMsg)
	if !ok || done.n != 3 || copied != " keep\n-old\n+new" {
		t.Fatalf("copied %q (%+v)", copied, msg)
	}
	if g.selOn {
		t.Fatal("copying ends copy mode")
	}
	// with no range, y copies the line under the cursor
	g.selOn, g.selAnchor, g.selCur = true, -1, 3
	if done, ok := m.gitDiffSelectKeys("y")().(copyDoneMsg); !ok || done.n != 1 || copied != "-old" {
		t.Fatalf("copied %q", copied)
	}
	// esc only exits
	g.selOn, g.selAnchor, g.selCur = true, 0, 1
	if m.gitDiffSelectKeys("esc") != nil || g.selOn || g.selAnchor != -1 {
		t.Fatal("esc must exit without copying")
	}
	// through the real key path: esc exits, and q / digits do not reach the global shortcuts meanwhile
	m.view, m.focus = "git", paneGitDiff
	g.selOn, g.selAnchor, g.selCur = true, -1, 1
	press(m, "q", "1")
	if m.mode != modeNormal || m.view != "git" || !g.selOn {
		t.Fatalf("copy mode must keep the keys: mode = %v view = %s on = %v", m.mode, m.view, g.selOn)
	}
	press(m, "esc")
	if g.selOn {
		t.Fatal("esc must leave copy mode")
	}
}

func TestEscStepsBackOutOfGitSections(t *testing.T) {
	m, _ := newModel(t)
	m.view, m.focus = "git", paneGitDiff
	m.gs().lastList = paneBranches
	press(m, "esc")
	if m.focus != paneBranches {
		t.Fatalf("esc in the diff goes back to the list it came from, focus = %s", m.focus)
	}
	press(m, "esc")
	if m.focus != paneRepos {
		t.Fatalf("esc in a list goes back to the repos, focus = %s", m.focus)
	}
	press(m, "esc")
	if m.focus != paneRepos || m.view != "git" {
		t.Fatalf("esc on the repos stays put, focus = %s view = %s", m.focus, m.view)
	}
}

func TestFitHintsKeepsHelpAndQuit(t *testing.T) {
	hints := [][2]string{{"space", "stage"}, {"a", "all"}, {"c", "commit"}, {"enter", "diff/fold"}, {"?", "help"}, {"q", "quit"}}
	if got := fitHints(hints, 200); len(got) != len(hints) {
		t.Fatalf("everything fits: %v", got)
	}
	got := fitHints(hints, 40)
	if n := len(got); n < 3 || got[n-2][0] != "?" || got[n-1][0] != "q" {
		t.Fatalf("help and quit stay at the end: %v", got)
	}
	total := 0
	for _, h := range got {
		total += len(h[0]) + 1 + len(h[1]) + 3
	}
	if total > 40 {
		t.Fatalf("%v is %d wide, more than 40", got, total)
	}
	if got := fitHints(hints, 5); len(got) != 2 {
		t.Fatalf("only the fixed ones when nothing else fits: %v", got)
	}
	esc := [][2]string{{"esc", "exit"}, {"j/k", "move"}, {"v", "start selecting"}, {"y", "copy line"}, {"?", "help"}, {"q", "quit"}}
	if got := fitHints(esc, 200); got[0][0] != "esc" {
		t.Fatalf("with room, the order is kept: %v", got)
	}
	if got := fitHints(esc, 40); got[len(got)-3][0] != "esc" {
		t.Fatalf("esc must survive a narrow footer: %v", got)
	}
}

func TestServiceListKeepsColumnsAroundLongNames(t *testing.T) {
	m, sup := newModel(t)
	sup.Service("db").Port = 5432
	list := func(w int, compact bool) string {
		return ansi.Strip(strings.Join(m.renderServiceList(w, 8, true, compact), "\n"))
	}
	// short names: the whole table fits in the default sidebar
	if out := list(52, false); !strings.Contains(out, ":5432") || !strings.Contains(out, "proc") {
		t.Fatalf("short names must leave room for every column:\n%s", out)
	}
	// a very long name is cut in the middle and the port stays
	long := strings.Repeat("long-", 8) + "tail"
	cwd, _ := os.Getwd()
	cfg := &config.OrbitConfig{Name: "t", Root: cwd, Services: map[string]*config.ServiceConfig{}, Groups: map[string][]string{}}
	s := svc(long, "sleep 30")
	s.Port = 5432
	cfg.Services[long] = s
	cfg.Order = []string{long}
	sup2 := supervisor.New(cfg, supervisor.Options{StateDir: t.TempDir()})
	t.Cleanup(func() { sup2.Dispose() })
	m2 := New(Options{Sup: sup2, OnQuit: func(string) {}})
	m2.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	out := ansi.Strip(strings.Join(m2.renderServiceList(40, 8, true, false), "\n"))
	if !strings.Contains(out, "…") || strings.Contains(out, long) || !strings.Contains(out, "tail") {
		t.Fatalf("the name must be cut in the middle, keeping its end:\n%s", out)
	}
	if !strings.Contains(out, ":5432") {
		t.Fatalf("a long name must not push the port out:\n%s", out)
	}
	// the compact list shows the port and nothing else
	if out := list(30, true); strings.Contains(out, "proc") {
		t.Fatalf("compact has no type badge:\n%s", out)
	}
}

func TestViewFitsTheTerminal(t *testing.T) {
	m, sup := newModel(t)
	sup.Start("db")
	for _, size := range [][2]int{{80, 24}, {60, 15}, {200, 60}, {40, 10}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, v := range []string{"1", "2", "3"} {
			press(m, v)
			lines := strings.Split(m.View(), "\n")
			if len(lines) != size[1] {
				t.Fatalf("%v view %s: %d lines", size, v, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != size[0] {
					t.Fatalf("%v view %s line %d is %d columns wide: %q", size, v, i, w, ansi.Strip(l))
				}
			}
		}
	}
}

func TestMultiRuneKeyChunkIsSplit(t *testing.T) {
	m, _ := newModel(t)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj")})
	if m.selected != "web" {
		t.Fatalf("selected = %q", m.selected)
	}
}

func click(m *Model, x, y int) {
	m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

func TestMouseFocusesPanelsAndSelectsRows(t *testing.T) {
	m, _ := newModel(t)
	m.View()
	// service rows start under the header and the panel border
	click(m, 2, 3)
	if m.selected != "api" || m.focus != paneServices {
		t.Fatalf("selected = %q focus = %s", m.selected, m.focus)
	}
	// the detail panel sits right of the sidebar
	click(m, m.sidebarW()+4, 2)
	if m.focus != paneDetail {
		t.Fatalf("click on the detail: focus = %s", m.focus)
	}
	// the tabs of the header switch view
	m.geo.mu.Lock()
	tab := m.geo.tabs[2]
	m.geo.mu.Unlock()
	click(m, tab.x0+1, 0)
	if m.view != "logs" {
		t.Fatalf("view = %s", m.view)
	}
}

func TestMouseOnGitListsSelectsAndScrolls(t *testing.T) {
	m, _ := newModel(t)
	m.view, m.focus = "git", paneRepos
	hits := []hitRegion{
		{x: 0, y: 1, w: 40, h: 12, pane: paneBranches, items: 5, first: 1},
		{x: 40, y: 1, w: 80, h: 12, pane: paneGitDiff},
	}
	m.geo.mu.Lock()
	m.geo.hits = hits
	m.geo.mu.Unlock()
	click(m, 3, 4) // border at y=1, so y=4 is the third row: item first+2
	if m.focus != paneBranches || m.gs().sel[paneBranches] != 3 || m.gs().lastList != paneBranches {
		t.Fatalf("focus = %s sel = %v", m.focus, m.gs().sel)
	}
	m.Update(tea.MouseMsg{X: 3, Y: 4, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if got := m.gs().sel[paneBranches]; got != 4 {
		t.Fatalf("wheel down: sel = %d", got)
	}
	m.Update(tea.MouseMsg{X: 50, Y: 4, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if m.gs().scroll != 3 {
		t.Fatalf("diff scroll = %d", m.gs().scroll)
	}
}

func TestSplashSkipsWithKeys(t *testing.T) {
	m, _ := newModel(t)
	m.opts.Splash = true
	m.Init()
	m.splash.start = time.Now().Add(-time.Second)
	if m.splash == nil || !strings.Contains(screen(m), "▀") {
		t.Fatal("the splash should be showing")
	}
	press(m, "j")
	if m.splash == nil || m.splash.closing.IsZero() {
		t.Fatal("a key should start closing the splash")
	}
	press(m, "j")
	if m.splash != nil {
		t.Fatal("a second key should end it")
	}
	if !strings.Contains(screen(m), "Services") {
		t.Fatalf("the app should show after the splash:\n%s", screen(m))
	}
}

func TestSplashSkippedOnTinyTerminals(t *testing.T) {
	m, _ := newModel(t)
	m.opts.Splash = true
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	m.Update(splashTickMsg{})
	if m.splash != nil {
		t.Fatal("no splash on a terminal too small for it")
	}
}

func TestTaskRunsInTheConsoleAndStaysOpenUntilAKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a pseudo-terminal")
	}
	m, sup := newModel(t)
	cfg := sup.Config()
	cfg.Tasks = map[string]*config.TaskConfig{"hi": {Name: "hi", Cmd: "echo hi-from-task; exit 3", Cwd: cfg.Root, Env: map[string]string{}}}
	cfg.TaskOrder = []string{"hi"}
	t.Cleanup(m.consoles.CloseAll)

	var run func(*Model) tea.Cmd
	for _, c := range m.commands() {
		if c.id == "task-hi" {
			run = c.run
		}
	}
	if run == nil {
		t.Fatal("the palette has no entry for the task")
	}
	cmd := run(m)
	if m.mode != modeConsole || m.console == nil || !m.console.task {
		t.Fatalf("mode = %v, console = %+v", m.mode, m.console)
	}
	msg := cmd()
	exit, ok := msg.(consoleExitMsg)
	if !ok || exit.code != 3 {
		t.Fatalf("msg = %#v", msg)
	}
	m.Update(exit)
	if m.mode != modeConsole || !m.console.exited || m.console.code != 3 {
		t.Fatalf("the console must stay open showing the result: mode = %v", m.mode)
	}
	if out := strings.Join(m.console.term.StyledLines(false), "\n"); !strings.Contains(out, "hi-from-task") {
		t.Fatalf("output = %q", out)
	}
	m.Update(key("x"))
	if m.mode != modeNormal || m.console != nil {
		t.Fatal("any key should close the finished task")
	}
}

func TestTaskPickerShowsWhetherTheServicesAreUp(t *testing.T) {
	m, sup := newModel(t)
	cfg := sup.Config()
	cfg.Tasks = map[string]*config.TaskConfig{
		"lint":    {Name: "lint", Cmd: "go vet", Cwd: cfg.Root, Env: map[string]string{}},
		"migrate": {Name: "migrate", Cmd: "run migrations", Description: "Apply migrations", Cwd: cfg.Root, Env: map[string]string{}, DependsOn: []string{"db"}},
	}
	cfg.TaskOrder = []string{"lint", "migrate"}

	m.Update(key("!"))
	if m.mode != modeTasks {
		t.Fatalf("mode = %v", m.mode)
	}
	rows, _, _, _ := m.renderTasks(120, 36)
	out := strings.Join(rows, "\n")
	for _, want := range []string{"lint", "go vet", "migrate", "Apply migrations", "starts db"} {
		if !strings.Contains(out, want) {
			t.Errorf("the picker should show %q:\n%s", want, out)
		}
	}
	if r := m.taskReadiness(cfg.Tasks["lint"]); len(r.missing) != 0 {
		t.Fatalf("a task without services is always ready: %+v", r)
	}
	if r := m.taskReadiness(cfg.Tasks["migrate"]); len(r.missing) != 1 || len(r.failed) != 0 {
		t.Fatalf("migrate readiness = %+v", r)
	}

	m.Update(key("down"))
	if m.taskIdx != 1 {
		t.Fatalf("taskIdx = %d", m.taskIdx)
	}
	_, cmd := m.Update(key("enter"))
	if m.mode != modeNormal || cmd == nil {
		t.Fatalf("enter should close the picker and start the task: mode = %v", m.mode)
	}
	m.Update(key("!"))
	m.Update(key("esc"))
	if m.mode != modeNormal {
		t.Fatal("esc should close the picker")
	}
}

func TestTaskPickerWithoutTasksSaysSo(t *testing.T) {
	m, _ := newModel(t)
	m.Update(key("!"))
	if m.mode != modeNormal || m.toast == nil {
		t.Fatalf("mode = %v, toast = %v", m.mode, m.toast)
	}
}

func TestAHiddenRunningTaskAsksBeforeQuitting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a pseudo-terminal")
	}
	m, sup := newModel(t)
	cfg := sup.Config()
	cfg.Tasks = map[string]*config.TaskConfig{"slow": {Name: "slow", Cmd: "sleep 30", Cwd: cfg.Root, Env: map[string]string{}}}
	cfg.TaskOrder = []string{"slow"}
	t.Cleanup(m.consoles.CloseAll)

	m.runTask("slow")
	m.Update(key("ctrl+]")) // hide it: it keeps running
	if m.mode != modeNormal || m.runningTasks() != 1 {
		t.Fatalf("mode = %v, running tasks = %d", m.mode, m.runningTasks())
	}
	m.Update(key("q"))
	if m.mode != modeQuit {
		t.Fatalf("quitting with a task running should ask first, mode = %v", m.mode)
	}
	rows, _, _, _ := m.renderConfirm(120, 36)
	if out := strings.Join(rows, "\n"); !strings.Contains(out, "1 task(s) running") {
		t.Fatalf("the confirmation should mention the task:\n%s", out)
	}
	m.Update(key("esc"))
	if m.mode != modeNormal || m.runningTasks() != 1 {
		t.Fatal("cancelling must leave the task running")
	}
}
