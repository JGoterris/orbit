package ui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

type fakeHost struct {
	mu       sync.Mutex
	infos    []plugins.Info
	badges   map[string][]plugins.TaggedBadge
	list     []protocol.Item
	sections []protocol.Section
	options  []protocol.Option
	result   protocol.InvokeResult
	subs     []func(plugins.Update)

	// pane, when set, answers for the panes it knows before the default ones
	pane func(protocol.ViewDataParams) (protocol.ViewData, bool)

	dataCalls   []protocol.ViewDataParams
	invokeCalls []protocol.InvokeParams
}

func (h *fakeHost) Plugins() []plugins.Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.infos
}
func (h *fakeHost) Badges() map[string][]plugins.TaggedBadge {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.badges
}
func (h *fakeHost) OnUpdate(fn func(plugins.Update)) func() {
	h.mu.Lock()
	h.subs = append(h.subs, fn)
	h.mu.Unlock()
	return func() {}
}

func (h *fakeHost) Data(_ string, p protocol.ViewDataParams) (protocol.ViewData, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dataCalls = append(h.dataCalls, p)
	if h.pane != nil {
		if d, ok := h.pane(p); ok {
			return d, nil
		}
	}
	switch p.Pane {
	case "list":
		return protocol.ViewData{List: &protocol.ListData{
			Columns: []protocol.Column{{ID: "title"}, {ID: "author", Priority: 1, Width: 8}},
			Items:   h.list,
		}}, nil
	case "info":
		return protocol.ViewData{Detail: &protocol.DetailData{Sections: []protocol.Section{{
			Title:  "MR " + p.Context.Selection["list"],
			Fields: []protocol.Field{{Label: "branch", Value: "feature/" + p.Context.Selection["list"], Tone: protocol.ToneInfo}},
		}}}}, nil
	}
	return protocol.ViewData{}, fmt.Errorf("no pane %s", p.Pane)
}

func (h *fakeHost) Invoke(_ string, p protocol.InvokeParams) (protocol.InvokeResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.invokeCalls = append(h.invokeCalls, p)
	return h.result, nil
}

func (h *fakeHost) Options(string, protocol.OptionsParams) ([]protocol.Option, error) {
	return h.options, nil
}

func (h *fakeHost) Sections(_ string, p protocol.SectionsParams) ([]protocol.Section, error) {
	return h.sections, nil
}

func (h *fakeHost) calls() []protocol.ViewDataParams {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]protocol.ViewDataParams(nil), h.dataCalls...)
}

type withHost struct {
	supervisor.Like
	host plugins.Host
}

func (w withHost) Plugins() plugins.Host { return w.host }

func forgeInfo() plugins.Info {
	return plugins.Info{
		Name: "forge", Version: "0.1.0", Status: plugins.StatusRunning,
		Contributes: protocol.Contributes{
			Views: []protocol.View{{
				ID: "prs", Title: "Pull requests",
				Panes: []protocol.Pane{
					{ID: "list", Widget: protocol.WidgetList, Title: "MR", Weight: 2},
					{ID: "info", Widget: protocol.WidgetDetail, Title: "Details", Bind: "list"},
				},
			}},
			Actions: []protocol.Action{
				{ID: "merge", Title: "Merge", Key: "M", Scope: protocol.Scope{View: "prs", Pane: "list"},
					Confirm: &protocol.Confirm{Title: "Merge {item} with {method}?", Danger: true},
					Inputs:  []protocol.Input{{Name: "method", Kind: protocol.InputPick, Options: []string{"squash", "merge", "rebase"}}}},
				{ID: "open", Title: "Open in browser", Key: "alt+o"},
				{ID: "prs-of", Title: "PRs of service", Scope: protocol.Scope{Service: true}},
			},
			ServiceSections: []protocol.SectionDef{{ID: "pr", Title: "PR"}},
			ServiceColumn:   &protocol.ColumnDef{ID: "pr", Width: 6},
			HeaderBadges:    true,
		},
	}
}

func newPluginModel(t *testing.T, host *fakeHost) *Model {
	t.Helper()
	host.mu.Lock()
	if host.infos == nil {
		host.infos = []plugins.Info{forgeInfo()}
	}
	if host.list == nil {
		host.list = []protocol.Item{
			{ID: "41", Cells: map[string]protocol.Cell{"title": {Text: "!41 Fix login"}, "author": {Text: "ana"}}, Icon: "open"},
			{ID: "42", Cells: map[string]protocol.Cell{"title": {Text: "!42 Add search"}, "author": {Text: "bo"}}, Icon: "draft"},
			{ID: "43", Cells: map[string]protocol.Cell{"title": {Text: "!43 Update deps"}, "author": {Text: "cy"}}, Icon: "merge"},
		}
	}
	host.mu.Unlock()
	base, _ := newModel(t)
	m := New(Options{Sup: withHost{Like: base.sup, host: host}, OnQuit: func(string) {}})
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 36})
	return m
}

// drive runs a command and feeds the messages it produces back into the model. Timers (toasts, ticks) are left
// alone: a command that does not answer at once is not what a test is after.
func drive(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(300 * time.Millisecond):
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			drive(m, c)
		}
		return
	}
	if msg == nil {
		return
	}
	_, next := m.Update(msg)
	drive(m, next)
}

func pressDrive(m *Model, keys ...string) {
	for _, k := range keys {
		_, cmd := m.Update(key(k))
		drive(m, cmd)
	}
}

func openForge(t *testing.T, host *fakeHost) *Model {
	t.Helper()
	m := newPluginModel(t, host)
	pressDrive(m, "5")
	return m
}

func TestPluginViewAppearsInTheHeaderAndLoads(t *testing.T) {
	host := &fakeHost{}
	m := newPluginModel(t, host)
	if s := screen(m); !strings.Contains(s, "5 Pull requests") {
		t.Fatalf("the tab of the plugin must be in the header:\n%s", s)
	}
	pressDrive(m, "5")
	if m.view != "p:forge/prs" {
		t.Fatalf("view = %q", m.view)
	}
	s := screen(m)
	for _, want := range []string{"MR · 3", "!41 Fix login", "ana", "Details", "MR 41", "feature/41"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the screen lacks %q:\n%s", want, s)
		}
	}
}

func TestPluginBoundPaneFollowsTheSelection(t *testing.T) {
	host := &fakeHost{}
	m := openForge(t, host)
	pressDrive(m, "j")
	if s := screen(m); !strings.Contains(s, "feature/42") {
		t.Fatalf("the detail must follow the selection:\n%s", s)
	}
	var last protocol.ViewDataParams
	for _, c := range host.calls() {
		if c.Pane == "info" {
			last = c
		}
	}
	if last.Context.Selection["list"] != "42" {
		t.Fatalf("the plugin must be told what is selected: %+v", last)
	}
	pressDrive(m, "G")
	if s := screen(m); !strings.Contains(s, "feature/43") {
		t.Fatalf("G goes to the last item:\n%s", s)
	}
}

func TestPluginSelectionSurvivesAReload(t *testing.T) {
	host := &fakeHost{}
	m := openForge(t, host)
	pressDrive(m, "j")
	// a new item arrives at the top: the bar must stay on !42, not on the row it was on
	host.mu.Lock()
	host.list = append([]protocol.Item{{ID: "50", Cells: map[string]protocol.Cell{"title": {Text: "!50 New"}}}}, host.list...)
	host.mu.Unlock()
	_, cmd := m.Update(pluginUpdateMsg{plugins.Update{Kind: plugins.UpdateInvalidate, Plugin: "forge"}})
	drive(m, cmd)
	pn := m.plug.panes["forge/prs/list"]
	if pn.sel != "42" {
		t.Fatalf("selection moved to %q after a reload", pn.sel)
	}
}

func TestPluginIgnoresAnswersToOldRequests(t *testing.T) {
	host := &fakeHost{}
	m := openForge(t, host)
	pn := m.plug.panes["forge/prs/list"]
	stale := pluginDataMsg{key: pn.key, req: pn.req - 1, data: protocol.ViewData{List: &protocol.ListData{Items: nil}}}
	m.Update(stale)
	if len(pn.vis) != 3 {
		t.Fatal("an answer to an old request must be dropped")
	}
}

func TestPluginFilter(t *testing.T) {
	host := &fakeHost{}
	m := openForge(t, host)
	pressDrive(m, "/", "s", "e", "a")
	s := screen(m)
	if !strings.Contains(s, "!42 Add search") || strings.Contains(s, "!41 Fix login") {
		t.Fatalf("the filter must narrow the list:\n%s", s)
	}
	if !strings.Contains(s, "MR · 1 · /sea") {
		t.Fatalf("the title must say what is filtered:\n%s", s)
	}
	pressDrive(m, "esc")
	if s := screen(m); !strings.Contains(s, "!41 Fix login") {
		t.Fatalf("esc clears the filter:\n%s", s)
	}
}

func TestPluginActionAsksThenRuns(t *testing.T) {
	host := &fakeHost{result: protocol.InvokeResult{Toast: &protocol.Toast{Text: "merged !42", Tone: protocol.ToneOK}}}
	m := openForge(t, host)
	pressDrive(m, "j", "M")
	if m.mode != modePlugin {
		t.Fatalf("the action must open its dialog, mode = %d", m.mode)
	}
	s := screen(m)
	for _, want := range []string{"Merge", "squash", "merge", "rebase"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the pick lacks %q:\n%s", want, s)
		}
	}
	pressDrive(m, "down", "enter")
	s = screen(m)
	if !strings.Contains(s, "Merge !42 Add search with merge?") {
		t.Fatalf("the question must name the item and the choice:\n%s", s)
	}
	if len(host.invokeCalls) != 0 {
		t.Fatal("nothing may run before the confirmation")
	}
	pressDrive(m, "y")
	if len(host.invokeCalls) != 1 {
		t.Fatalf("the action did not run: %+v", host.invokeCalls)
	}
	call := host.invokeCalls[0]
	if call.Action != "merge" || call.Inputs["method"] != "merge" || call.Context.Selection["list"] != "42" {
		t.Fatalf("the plugin got %+v", call)
	}
	if m.toast == nil || m.toast.text != "merged !42" {
		t.Fatalf("the toast of the plugin must show: %+v", m.toast)
	}
	if m.mode != modeNormal {
		t.Fatal("the dialog must close")
	}
}

func TestPluginActionCanBeCancelled(t *testing.T) {
	for _, keys := range [][]string{{"M", "esc"}, {"M", "enter", "n"}, {"M", "enter", "esc"}} {
		host := &fakeHost{}
		m := openForge(t, host)
		pressDrive(m, keys...)
		if len(host.invokeCalls) != 0 || m.mode != modeNormal || m.plug.dialog != nil {
			t.Fatalf("%v: it must cancel without running (calls %d, mode %d)", keys, len(host.invokeCalls), m.mode)
		}
	}
}

func TestPluginActionWithoutQuestionsRunsAtOnceFromAnyView(t *testing.T) {
	host := &fakeHost{}
	m := newPluginModel(t, host)
	// alt+o has no scope: it works in the dashboard too
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o"), Alt: true})
	drive(m, cmd)
	if len(host.invokeCalls) != 1 || host.invokeCalls[0].Action != "open" {
		t.Fatalf("calls: %+v", host.invokeCalls)
	}
	if host.invokeCalls[0].Context.Service == "" {
		t.Fatal("the selected service must go along")
	}
}

func TestPluginActionOfAViewIsNotAvailableElsewhere(t *testing.T) {
	host := &fakeHost{}
	m := newPluginModel(t, host)
	pressDrive(m, "M")
	if m.mode == modePlugin || len(host.invokeCalls) != 0 {
		t.Fatal("M belongs to the view of the plugin")
	}
}

func TestPluginActionsInThePalette(t *testing.T) {
	host := &fakeHost{}
	m := newPluginModel(t, host)
	var labels []string
	for _, c := range m.commands() {
		labels = append(labels, c.label)
	}
	all := strings.Join(labels, "\n")
	for _, want := range []string{"View: Pull requests", "Forge: Open in browser", "Forge: PRs of service · api"} {
		if !strings.Contains(all, want) {
			t.Fatalf("the palette lacks %q", want)
		}
	}
	if strings.Contains(all, "Forge: Merge") {
		t.Fatal("an action of a view must not be offered elsewhere")
	}
	pressDrive(m, "5")
	labels = nil
	for _, c := range m.commands() {
		labels = append(labels, c.label)
	}
	if !strings.Contains(strings.Join(labels, "\n"), "Forge: Merge") {
		t.Fatal("it must be offered inside its view")
	}
}

func TestPluginKeysAreResolved(t *testing.T) {
	info := forgeInfo()
	info.Contributes.Actions = append(info.Contributes.Actions,
		protocol.Action{ID: "quit", Title: "Quit?", Key: "q", Scope: protocol.Scope{View: "prs"}},
		protocol.Action{ID: "dup", Title: "Dup", Key: "M", Scope: protocol.Scope{View: "prs", Pane: "list"}},
	)
	actions, problems := ResolvePluginKeys([]plugins.Info{info}, map[string]string{"forge.open": "alt+x", "forge.prs-of": "alt+p"})
	keys := map[string]string{}
	for _, a := range actions {
		keys[a.def.ID] = a.key
	}
	if keys["merge"] != "M" || keys["open"] != "alt+x" || keys["prs-of"] != "alt+p" {
		t.Fatalf("keys = %v", keys)
	}
	if keys["quit"] != "" || keys["dup"] != "" {
		t.Fatalf("a key of orbit and a duplicate must be dropped: %v", keys)
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %v", problems)
	}
	actions, _ = ResolvePluginKeys([]plugins.Info{forgeInfo()}, map[string]string{"forge.merge": "none"})
	for _, a := range actions {
		if a.def.ID == "merge" && a.key != "" {
			t.Fatal(`"none" must remove the key`)
		}
	}
}

func TestPluginBadgesAndColumn(t *testing.T) {
	host := &fakeHost{badges: map[string][]plugins.TaggedBadge{
		"service:api": {{Plugin: "forge", Badge: protocol.Badge{Text: "!41", Tone: protocol.ToneOK, Icon: "ok"}}},
		"header":      {{Plugin: "forge", Badge: protocol.Badge{Text: "CI 2", Tone: protocol.ToneError, Icon: "fail", Action: "open"}}},
	}}
	m := newPluginModel(t, host)
	s := screen(m)
	if !strings.Contains(s, "✓ !41") {
		t.Fatalf("the badge of the service must be in the list:\n%s", s)
	}
	header := strings.Split(s, "\n")[0]
	if !strings.Contains(header, "✖ CI 2") {
		t.Fatalf("the badge of the header is missing: %q", header)
	}
	if len(m.geo.badges) != 1 || m.geo.badges[0].action != "open" {
		t.Fatalf("a badge with an action must be clickable: %+v", m.geo.badges)
	}
	_, cmd := m.Update(tea.MouseMsg{X: m.geo.badges[0].x0 + 1, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	drive(m, cmd)
	if len(host.invokeCalls) != 1 || host.invokeCalls[0].Action != "open" {
		t.Fatalf("clicking the badge must run its action: %+v", host.invokeCalls)
	}
}

func TestBadgesNeverOverflowTheirColumn(t *testing.T) {
	m := newPluginModel(t, &fakeHost{})
	l := m.badgeLine("", []protocol.Badge{{Text: "a-badge-much-wider-than-the-column", Icon: "ok"}, {Text: "second"}}, 8)
	if l.width() != 8 {
		t.Fatalf("width = %d, want 8", l.width())
	}
	l = m.badgeLine("", []protocol.Badge{{Text: "ab"}, {Text: "cd"}, {Text: "ef"}}, 6)
	if l.width() != 6 || !strings.Contains(ansi.Strip(l.String()), "ab cd") || strings.Contains(ansi.Strip(l.String()), "ef") {
		t.Fatalf("badges that do not fit are left out whole: %q", ansi.Strip(l.String()))
	}
}

func TestPluginSectionsInTheServiceDetail(t *testing.T) {
	host := &fakeHost{sections: []protocol.Section{{Fields: []protocol.Field{{Label: "open MR", Value: "!41 Fix login"}}}}}
	m := newPluginModel(t, host)
	pressDrive(m, "j") // selects another service, which asks again
	if s := screen(m); !strings.Contains(s, "open MR") || !strings.Contains(s, "!41 Fix login") {
		t.Fatalf("the detail must show what the plugin adds:\n%s", s)
	}
	if !strings.Contains(screen(m), "── PR") {
		t.Fatal("the block takes the title of the manifest when the plugin gives none")
	}
}

func TestPluginThatFailedSaysWhy(t *testing.T) {
	host := &fakeHost{}
	m := newPluginModel(t, host)
	host.mu.Lock()
	info := forgeInfo()
	info.Status, info.Error = plugins.StatusFailed, "cannot start ./orbit-forge: no such file"
	host.infos = []plugins.Info{info}
	host.mu.Unlock()
	cmd := m.onPluginUpdate(plugins.Update{Kind: plugins.UpdateStatus, Plugin: "forge"})
	if m.toast == nil || !strings.Contains(m.toast.text, "failed") || !strings.Contains(m.toast.text, "orbit plugin logs forge") {
		t.Fatalf("the failure must be reported once: %+v", m.toast)
	}
	_ = cmd
	pressDrive(m, "5")
	s := screen(m)
	if !strings.Contains(s, "plugin forge is not running") || !strings.Contains(s, "cannot start") {
		t.Fatalf("the view must say why:\n%s", s)
	}
	if !strings.Contains(strings.Split(s, "\n")[0], "✖ plugin forge") {
		t.Fatal("the header must show the failure")
	}
	m.toast = nil
	m.onPluginUpdate(plugins.Update{Kind: plugins.UpdateStatus, Plugin: "forge"})
	if m.toast != nil {
		t.Fatal("the same failure must not be reported twice")
	}
}

func TestPluginMissingFromTheProjectIsReported(t *testing.T) {
	host := &fakeHost{infos: []plugins.Info{{Name: "kube", Missing: true, Status: plugins.StatusFailed}}}
	m := newPluginModel(t, host)
	if header := strings.Split(screen(m), "\n")[0]; !strings.Contains(header, "⚠ plugin kube missing") {
		t.Fatalf("header: %q", header)
	}
	if len(m.allViews()) != len(views) {
		t.Fatal("a plugin that is not installed has no views")
	}
}

func TestPluginViewOnANarrowTerminal(t *testing.T) {
	host := &fakeHost{}
	m := openForge(t, host)
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	s := screen(m)
	if !strings.Contains(s, "MR") || strings.Contains(s, "Details") {
		t.Fatalf("a narrow terminal shows only the focused pane:\n%s", s)
	}
	pressDrive(m, "tab")
	if s := screen(m); !strings.Contains(s, "Details") {
		t.Fatalf("tab must reach the other pane:\n%s", s)
	}
}

func TestPluginHintsAndHelp(t *testing.T) {
	host := &fakeHost{}
	m := openForge(t, host)
	footer := ""
	for _, l := range strings.Split(screen(m), "\n") {
		footer = l
	}
	if !strings.Contains(footer, "M") || !strings.Contains(footer, "merge") {
		t.Fatalf("the hints must show the actions of the pane: %q", footer)
	}
	// in the view of the plugin, its keys come first
	pressDrive(m, "?")
	help := screen(m)
	if !strings.Contains(help, "Pull requests (plugin forge)") || !strings.Contains(help, "(forge prs) M") {
		t.Fatalf("the help must list the plugin:\n%s", help)
	}
}

func TestPluginTextIsNeverStyledByThePlugin(t *testing.T) {
	host := &fakeHost{}
	m := newPluginModel(t, host)
	spans := m.pluginTextLines(&protocol.TextData{Lines: [][]protocol.Span{{{Text: "a", Tone: "bogus"}, {Text: "b", Tone: protocol.ToneError, Bold: true}}}}, 0)
	if len(spans) != 1 || !strings.Contains(spans[0], "a") {
		t.Fatalf("lines = %q", spans)
	}
}
