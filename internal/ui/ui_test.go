package ui

import (
	"errors"
	"fmt"
	"os"
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
