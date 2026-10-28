package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

func TestWrapSpans(t *testing.T) {
	text := func(lines [][]protocol.Span) []string {
		var out []string
		for _, l := range lines {
			out = append(out, spansText(l))
		}
		return out
	}
	if got := wrapSpans([]protocol.Span{{Text: "short"}}, 20); len(got) != 1 || spansText(got[0]) != "short" {
		t.Fatalf("a line that fits stays as it is: %q", text(got))
	}
	got := text(wrapSpans([]protocol.Span{{Text: "the quick brown fox jumps over the lazy dog"}}, 12))
	want := []string{"the quick", "brown fox", "jumps over", "the lazy dog"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("broken at the spaces: %q", got)
	}
	got = text(wrapSpans([]protocol.Span{{Text: "  - a list item that is long"}}, 14))
	for _, l := range got[1:] {
		if !strings.HasPrefix(l, "    ") {
			t.Fatalf("a continuation keeps the indentation of the item: %q", got)
		}
	}
	got = text(wrapSpans([]protocol.Span{{Text: strings.Repeat("x", 25)}}, 10))
	if len(got) != 3 || got[0] != strings.Repeat("x", 10) {
		t.Fatalf("a word wider than the line is cut: %q", got)
	}
	tones := wrapSpans([]protocol.Span{{Text: "error: ", Tone: protocol.ToneError, Bold: true}, {Text: "something went quite wrong"}}, 15)
	if tones[0][0].Tone != protocol.ToneError || !tones[0][0].Bold {
		t.Fatalf("the tones stay with their text: %+v", tones)
	}
	for _, l := range tones {
		if w := ansi.StringWidth(spansText(l)); w > 15 {
			t.Fatalf("a line is %d wide: %+v", w, tones)
		}
	}
}

const longBody = "This merge request rewrites the login flow so that the session survives a restart of the server, and it ends with THE-END"

func longHost() *fakeHost {
	return &fakeHost{pane: func(p protocol.ViewDataParams) (protocol.ViewData, bool) {
		if p.Pane != "info" {
			return protocol.ViewData{}, false
		}
		return protocol.ViewData{Detail: &protocol.DetailData{Sections: []protocol.Section{{
			Title:  "MR " + p.Context.Selection["list"] + " " + longBody,
			Fields: []protocol.Field{{Label: "branch", Value: "feature/" + p.Context.Selection["list"]}, {Label: "labels", Value: longBody}},
			Text:   [][]protocol.Span{{{Text: longBody, Tone: protocol.ToneMuted}}},
		}}}}, true
	}}
}

func TestPluginDetailWrapsInsteadOfCutting(t *testing.T) {
	m := openForge(t, longHost())
	s := screen(m)
	if n := strings.Count(s, "THE-END"); n < 3 {
		t.Fatalf("the title, the field and the text must be whole (%d of 3):\n%s", n, s)
	}
	if strings.Contains(s, "flow so…") {
		t.Fatalf("nothing is cut with an ellipsis:\n%s", s)
	}
}

func TestPluginTextWrapsWhenThePaneSaysSo(t *testing.T) {
	m := newPluginModel(t, &fakeHost{})
	lines := m.pluginTextLines(&protocol.TextData{Lines: [][]protocol.Span{{{Text: longBody}}}}, 30)
	if len(lines) < 4 || !strings.Contains(ansi.Strip(lines[len(lines)-1]), "THE-END") {
		t.Fatalf("lines = %q", lines)
	}
	if lines := m.pluginTextLines(&protocol.TextData{Lines: [][]protocol.Span{{{Text: longBody}}}}, 0); len(lines) != 1 {
		t.Fatalf("without wrap a line stays one: %q", lines)
	}
}

func TestPluginSelectedRowShowsItsWholeTitle(t *testing.T) {
	host := &fakeHost{list: []protocol.Item{
		{ID: "41", Cells: map[string]protocol.Cell{"title": {Text: "!41 " + longBody}, "author": {Text: "ana"}}},
		{ID: "42", Cells: map[string]protocol.Cell{"title": {Text: "!42 Add search"}, "author": {Text: "bo"}}},
	}}
	m := openForge(t, host)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	s := screen(m)
	if !strings.Contains(s, "THE-END") {
		t.Fatalf("the selected row must show its whole title:\n%s", s)
	}
	pn := m.plug.panes["forge/prs/list"]
	if len(pn.lineItems) < 3 || pn.lineItems[len(pn.lineItems)-1] != 1 {
		t.Fatalf("the lines must know their rows: %v", pn.lineItems)
	}
	pressDrive(m, "j")
	if s := screen(m); strings.Contains(s, "THE-END") && strings.Count(s, "THE-END") > 2 {
		t.Fatalf("a row that is not selected takes one line:\n%s", s)
	}
}

func TestPluginItemFullScreen(t *testing.T) {
	host := &fakeHost{}
	m := openForge(t, host)
	pressDrive(m, "enter")
	s := screen(m)
	if !strings.Contains(s, "Details · 1 of 3") || strings.Contains(s, "MR · 3") || !strings.Contains(s, "feature/41") {
		t.Fatalf("enter opens the item full screen:\n%s", s)
	}
	if strings.Contains(s, "api") && strings.Contains(s, "worker") {
		t.Fatalf("the services are not shown:\n%s", s)
	}
	pressDrive(m, "]")
	if s := screen(m); !strings.Contains(s, "Details · 2 of 3") || !strings.Contains(s, "feature/42") {
		t.Fatalf("] goes to the next item without leaving:\n%s", s)
	}
	pressDrive(m, "[", "[")
	if s := screen(m); !strings.Contains(s, "Details · 1 of 3") {
		t.Fatalf("[ goes back and stops at the first:\n%s", s)
	}
	pressDrive(m, "tab")
	if m.pluginItemView() == nil {
		t.Fatal("tab keeps to the panes of the item")
	}
	pressDrive(m, "esc")
	if m.zoomed || m.focus != pane(pluginViewPrefix+"forge/prs/list") {
		t.Fatalf("esc goes back to the list: zoomed=%v focus=%s", m.zoomed, m.focus)
	}
}

func TestPluginActionOfTheListRunsFromItsDetail(t *testing.T) {
	host := &fakeHost{}
	m := openForge(t, host)
	pressDrive(m, "j", "enter", "M")
	if m.mode != modePlugin {
		t.Fatal("the action of the list must run from the pane that follows it")
	}
	pressDrive(m, "enter")
	if s := screen(m); !strings.Contains(s, "Merge !42 Add search with squash?") {
		t.Fatalf("the question names the item of the list:\n%s", s)
	}
}

func TestPluginSizedPaneIsAskedAgainWithItsWidth(t *testing.T) {
	info := forgeInfo()
	info.Contributes.Views[0].Panes = append(info.Contributes.Views[0].Panes,
		protocol.Pane{ID: "graph", Widget: protocol.WidgetText, Bind: "list", Sized: true})
	host := &fakeHost{infos: []plugins.Info{info}, pane: func(p protocol.ViewDataParams) (protocol.ViewData, bool) {
		if p.Pane != "graph" {
			return protocol.ViewData{}, false
		}
		return protocol.ViewData{Text: &protocol.TextData{Lines: [][]protocol.Span{{{Text: "graph"}}}}}, true
	}}
	m := openForge(t, host)
	widths := func() []int {
		var out []int
		for _, c := range host.calls() {
			if c.Pane == "graph" {
				out = append(out, c.Context.Width)
			}
		}
		return out
	}
	screen(m)
	drive(m, m.pluginResized())
	got := widths()
	if len(got) < 2 || got[len(got)-1] == 0 {
		t.Fatalf("once drawn, a sized pane is asked again with its width: %v", got)
	}
	before := len(got)
	screen(m)
	drive(m, m.pluginResized())
	if len(widths()) != before {
		t.Fatalf("not again while the width is the same: %v", widths())
	}
	pressDrive(m, "enter")
	screen(m)
	drive(m, m.pluginResized())
	if w := widths(); len(w) == before || w[len(w)-1] <= got[len(got)-1] {
		t.Fatalf("full screen it is wider: %v", w)
	}
}

func TestPluginItemActionNeedsAnItem(t *testing.T) {
	info := forgeInfo()
	info.Contributes.Actions[0].Item = true
	host := &fakeHost{infos: []plugins.Info{info}, list: []protocol.Item{}}
	m := openForge(t, host)
	footer := ""
	for _, l := range strings.Split(screen(m), "\n") {
		footer = l
	}
	if strings.Contains(footer, "merge") {
		t.Fatalf("an action about an item is not offered with none: %q", footer)
	}
	pressDrive(m, "M")
	if m.mode == modePlugin || len(host.invokeCalls) != 0 {
		t.Fatal("nor does it run")
	}
}

func TestPluginZoomSaysHowToGoBack(t *testing.T) {
	m := openForge(t, &fakeHost{})
	pressDrive(m, "z")
	footer := ""
	for _, l := range strings.Split(screen(m), "\n") {
		footer = l
	}
	if !strings.Contains(footer, "esc back") {
		t.Fatalf("hints = %q", footer)
	}
}

func TestPluginBoundPaneSaysLoadingForANewSelection(t *testing.T) {
	m := openForge(t, &fakeHost{})
	pn := m.plug.panes["forge/prs/list"]
	pn.move(1)
	m.pluginSelectionChanged(pn)
	if s := screen(m); strings.Contains(s, "feature/41") || !strings.Contains(s, "loading") {
		t.Fatalf("the detail of the last item must not stay while the new one loads:\n%s", s)
	}
}

func TestWrapBreaksALongPathAfterASlash(t *testing.T) {
	got := wrapSpans([]protocol.Span{{Text: "branch dependabot/go_modules/go-modules-49be9fcf9a"}}, 30)
	if spansText(got[0]) != "branch dependabot/go_modules/" {
		t.Fatalf("got %q", spansText(got[0]))
	}
}

func TestPluginTextInputIsSeenWhateverItsLabel(t *testing.T) {
	info := forgeInfo()
	info.Contributes.Actions = append(info.Contributes.Actions, protocol.Action{ID: "comment", Title: "Comment", Key: "m",
		Scope: protocol.Scope{View: "prs", Pane: "list"}, Inputs: []protocol.Input{{Name: "body", Kind: protocol.InputText, Title: "Comment on {item}"}}})
	host := &fakeHost{infos: []plugins.Info{info}, list: []protocol.Item{
		{ID: "8", Cells: map[string]protocol.Cell{"title": {Text: "#8 " + longBody}}},
	}}
	m := openForge(t, host)
	pressDrive(m, "m", "h", "o", "l", "a")
	if s := screen(m); !strings.Contains(s, "hola") {
		t.Fatalf("what is typed must show even under a long label:\n%s", s)
	}
}
