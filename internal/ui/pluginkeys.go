package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/internal/ui/theme"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// pluginActionForKey is the action the key runs where the user is: one of the view and pane on screen, or one of no
// view at all.
func (m *Model) pluginActionForKey(s string) *pluginAction {
	vd := m.viewDef()
	var global *pluginAction
	for i := range m.plug.actions {
		a := &m.plug.actions[i]
		if a.key != s {
			continue
		}
		switch {
		case a.def.Scope.View == "":
			if global == nil {
				global = a
			}
		case vd.plugin != nil && vd.plugin.plugin == a.plugin && vd.plugin.def.ID == a.def.Scope.View:
			if a.def.Scope.Pane == "" {
				return a
			}
			if pn := m.focusedPluginPane(); pn != nil && pn.def.ID == a.def.Scope.Pane {
				return a
			}
		}
	}
	return global
}

func (m *Model) pluginPaneKeys(k tea.KeyMsg) (tea.Cmd, bool) {
	vd := m.viewDef()
	s := k.String()
	if s == "ctrl+r" {
		return tea.Batch(m.pluginLoadView(vd, true), m.notify("refreshing "+vd.label, theme.Active().Muted)), true
	}
	pn := m.focusedPluginPane()
	if pn == nil {
		return nil, false
	}
	if pn.def.Widget != protocol.WidgetList {
		return m.pluginScrollKey(pn, s)
	}
	step := 0
	switch s {
	case "down", "j":
		step = 1
	case "up", "k":
		step = -1
	case "ctrl+d", "pgdown":
		step = max(1, pn.page/2)
	case "ctrl+u", "pgup":
		step = -max(1, pn.page/2)
	case "g", "home":
		step = -len(pn.vis)
	case "G", "end":
		step = len(pn.vis)
	case "/":
		m.plug.filtering = pn
		m.plug.filterBar.set(pn.filter)
		return nil, true
	default:
		return nil, false
	}
	return m.pluginMove(pn, pn.idx+step), true
}

func (m *Model) pluginMove(pn *pluginPane, to int) tea.Cmd {
	if pn.move(to) {
		return m.pluginSelectionChanged(pn)
	}
	return nil
}

func (m *Model) pluginScrollKey(pn *pluginPane, s string) (tea.Cmd, bool) {
	page := max(1, pn.page)
	switch s {
	case "down", "j":
		pn.scrollTo(pn.scroll + 1)
	case "up", "k":
		pn.scrollTo(pn.scroll - 1)
	case "ctrl+d", "pgdown":
		pn.scrollTo(pn.scroll + page/2)
	case "ctrl+u", "pgup":
		pn.scrollTo(pn.scroll - page/2)
	case "g", "home":
		pn.scrollTo(0)
	case "G", "end":
		pn.scrollTo(pn.total)
	default:
		return nil, false
	}
	return nil, true
}

func (m *Model) pluginFilterKeys(k tea.KeyMsg) tea.Cmd {
	pn := m.plug.filtering
	switch k.String() {
	case "esc":
		m.plug.filtering = nil
		pn.filter = ""
	case "enter":
		m.plug.filtering = nil
		return nil
	case "ctrl+c":
		return m.requestQuit()
	default:
		m.plug.filterBar.handle(k)
		pn.filter = m.plug.filterBar.value()
	}
	if pn.refresh() {
		return m.pluginSelectionChanged(pn)
	}
	return nil
}

func (m *Model) pluginClick(r hitRegion, y int) tea.Cmd {
	m.focus = r.pane
	pn := m.plug.panes[strings.TrimPrefix(string(r.pane), pluginViewPrefix)]
	if pn == nil || r.items == 0 {
		return nil
	}
	row := y - r.y - 1
	if row < 0 || r.first+row >= r.items {
		return nil
	}
	return m.pluginMove(pn, r.first+row)
}

func (m *Model) pluginWheel(r hitRegion, delta int) tea.Cmd {
	pn := m.plug.panes[strings.TrimPrefix(string(r.pane), pluginViewPrefix)]
	if pn == nil {
		return nil
	}
	step := -delta / 3
	if pn.def.Widget == protocol.WidgetList {
		return m.pluginMove(pn, pn.idx+step)
	}
	pn.scrollTo(pn.scroll + step*3)
	return nil
}

// pluginDialog is the question an action asks before it runs: the inputs it needs, one after the other, and then
// its confirmation. orbit draws them, so they look like the rest of orbit whatever the plugin is.
type pluginDialog struct {
	plugin string
	def    protocol.Action
	ctx    protocol.Context
	item   string
	values map[string]string

	step      int
	confirmed bool
	kind      string // "text" | "pick" | "confirm"
	text      input
	filter    input
	options   []protocol.Option
	idx       int
	loading   bool
	err       string
}

type pluginOptionsMsg struct {
	d       *pluginDialog
	options []protocol.Option
	err     error
}

type pluginInvokedMsg struct {
	plugin string
	title  string
	res    protocol.InvokeResult
	err    error
}

func (m *Model) pluginRun(a *pluginAction, service string) tea.Cmd {
	p := theme.Active()
	if info, ok := m.pluginInfo(a.plugin); !ok || info.Status != plugins.StatusRunning {
		return m.notify(fmt.Sprintf("plugin %s is not running", a.plugin), p.Yellow)
	}
	if m.plug.running != "" {
		return m.notify("still running: "+m.plug.running, p.Yellow)
	}
	vd := m.viewDef()
	d := &pluginDialog{plugin: a.plugin, def: a.def, values: map[string]string{}}
	if vd.plugin != nil && vd.plugin.plugin == a.plugin {
		d.ctx = m.pluginContext(vd)
		if pn := m.focusedPluginPane(); pn != nil {
			d.item = pluginItemText(pn)
		}
	}
	if service != "" {
		d.ctx.Service = service
	}
	m.plug.dialog = d
	return m.pluginDialogAdvance()
}

func (d *pluginDialog) expand(s string) string {
	pairs := []string{"{item}", d.item, "{service}", d.ctx.Service}
	for k, v := range d.values {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

func (m *Model) pluginDialogAdvance() tea.Cmd {
	d := m.plug.dialog
	if d.step < len(d.def.Inputs) {
		in := d.def.Inputs[d.step]
		m.mode = modePlugin
		d.err = ""
		if in.Kind == protocol.InputText {
			d.kind = "text"
			d.text.set(d.expand(in.Default))
			return nil
		}
		d.kind, d.filter, d.idx = "pick", input{}, 0
		if !in.OptionsFrom {
			d.options = nil
			for i, o := range in.Options {
				d.options = append(d.options, protocol.Option{Value: o, Label: o})
				if o == in.Default {
					d.idx = i
				}
			}
			return nil
		}
		d.loading, d.options = true, nil
		host := m.sup.Plugins()
		req := protocol.OptionsParams{Action: d.def.ID, Input: in.Name, Context: d.ctx}
		return func() tea.Msg {
			opts, err := host.Options(d.plugin, req)
			return pluginOptionsMsg{d: d, options: opts, err: err}
		}
	}
	if d.def.Confirm != nil && !d.confirmed {
		d.kind = "confirm"
		m.mode = modePlugin
		return nil
	}
	m.plug.dialog = nil
	m.mode = modeNormal
	return m.pluginInvoke(d)
}

func (m *Model) pluginInvoke(d *pluginDialog) tea.Cmd {
	m.plug.running = d.plugin + ": " + d.def.Title
	host := m.sup.Plugins()
	req := protocol.InvokeParams{Action: d.def.ID, Context: d.ctx, Inputs: d.values}
	return func() tea.Msg {
		res, err := host.Invoke(d.plugin, req)
		return pluginInvokedMsg{plugin: d.plugin, title: d.def.Title, res: res, err: err}
	}
}

func (m *Model) onPluginOptions(msg pluginOptionsMsg) {
	d := msg.d
	if m.plug.dialog != d {
		return // cancelled while it loaded
	}
	d.loading = false
	if msg.err != nil {
		d.err = msg.err.Error()
		return
	}
	d.options = msg.options
	if len(d.options) == 0 {
		d.err = "nothing to choose from"
	}
}

func (m *Model) onPluginInvoked(msg pluginInvokedMsg) tea.Cmd {
	p := theme.Active()
	m.plug.running = ""
	var cmds []tea.Cmd
	if msg.err != nil {
		cmds = append(cmds, m.notify(msg.title+": "+firstLine(msg.err.Error()), p.Red))
	} else if t := msg.res.Toast; t != nil {
		cmds = append(cmds, m.notify(t.Text, m.toneColor(t.Tone)))
	} else {
		cmds = append(cmds, m.notify("✓ "+msg.title, p.Green))
	}
	if slices.Contains(msg.res.Invalidate, "none") {
		return tea.Batch(cmds...)
	}
	targets := msg.res.Invalidate
	if len(targets) == 0 || slices.Contains(targets, "*") {
		targets = []string{""}
	}
	for _, t := range targets {
		inv := protocol.Invalidate{}
		inv.View, inv.Pane, _ = strings.Cut(t, "/")
		cmds = append(cmds, m.onPluginInvalidate(plugins.Update{Kind: plugins.UpdateInvalidate, Plugin: msg.plugin, Invalidate: &inv}))
	}
	return tea.Batch(cmds...)
}

func (m *Model) keyInPluginDialog(k tea.KeyMsg) tea.Cmd {
	d := m.plug.dialog
	s := k.String()
	if d == nil {
		m.mode = modeNormal
		return nil
	}
	cancel := func() tea.Cmd {
		m.plug.dialog = nil
		m.mode = modeNormal
		return nil
	}
	if s == "ctrl+c" {
		return cancel()
	}
	switch d.kind {
	case "confirm":
		switch s {
		case "y", "Y", "enter":
			d.confirmed = true
			return m.pluginDialogAdvance()
		case "n", "N", "esc":
			return cancel()
		}
	case "text":
		switch s {
		case "esc":
			return cancel()
		case "enter":
			d.values[d.def.Inputs[d.step].Name] = d.text.value()
			d.step++
			return m.pluginDialogAdvance()
		}
		d.text.handle(k)
	case "pick":
		opts := d.filtered()
		switch s {
		case "esc":
			return cancel()
		case "up", "ctrl+p":
			d.idx = max(0, d.idx-1)
		case "down", "ctrl+n":
			d.idx = min(len(opts)-1, d.idx+1)
		case "enter":
			if d.loading || len(opts) == 0 {
				return nil
			}
			d.values[d.def.Inputs[d.step].Name] = opts[min(d.idx, len(opts)-1)].Value
			d.step++
			return m.pluginDialogAdvance()
		default:
			if d.filter.handle(k) {
				d.idx = 0
			}
		}
	}
	return nil
}

func (d *pluginDialog) filtered() []protocol.Option {
	q := d.filter.value()
	if q == "" {
		return d.options
	}
	type scored struct {
		o protocol.Option
		s int
	}
	var hits []scored
	for _, o := range d.options {
		if s, ok := fuzzyScore(q, o.Label+" "+o.Value); ok {
			hits = append(hits, scored{o, s})
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int { return a.s - b.s })
	out := make([]protocol.Option, len(hits))
	for i, h := range hits {
		out[i] = h.o
	}
	return out
}

func (m *Model) renderPluginDialog(W, H int) ([]string, int, int, int) {
	p := theme.Active()
	d := m.plug.dialog
	if d == nil {
		return nil, 0, 0, 0
	}
	w := min(70, W-4)
	inner := w - 4
	line := func(color, text string) string { return newLine(p.PanelAlt).add(color, text).String() }
	var content []string
	title := d.def.Title
	switch d.kind {
	case "confirm":
		color := p.Text
		if d.def.Confirm.Danger {
			color = p.Orange
		}
		for _, ln := range wrapText(d.expand(d.def.Confirm.Title), inner) {
			content = append(content, line(color, ln))
		}
		for _, in := range d.def.Inputs {
			if v, ok := d.values[in.Name]; ok {
				label := in.Title
				if label == "" {
					label = in.Name
				}
				content = append(content, newLine(p.PanelAlt).add(p.Dim, label+" ").add(p.Muted, v).String())
			}
		}
		content = append(content, "", line(p.Dim, "y / enter confirm · n / esc cancel"))
	case "text":
		in := d.def.Inputs[d.step]
		label := in.Title
		if label == "" {
			label = in.Name
		}
		l := newLine(p.PanelAlt).add(p.Accent, d.expand(label)+" ")
		fieldW := max(1, inner-ansi.StringWidth(d.expand(label))-1)
		l.raw(d.text.viewWithPlaceholder(fieldW, "", p.Text, p.Dim, p.PanelAlt, true), fieldW)
		content = append(content, l.String(), "", line(p.Dim, "enter ok · esc cancel"))
	case "pick":
		in := d.def.Inputs[d.step]
		label := in.Title
		if label == "" {
			label = in.Name
		}
		l := newLine(p.PanelAlt).add(p.Accent, d.expand(label)+" ")
		fieldW := max(1, inner-ansi.StringWidth(d.expand(label))-1)
		l.raw(d.filter.viewWithPlaceholder(fieldW, "type to search", p.Text, p.Dim, p.PanelAlt, true), fieldW)
		content = append(content, l.String())
		switch {
		case d.loading:
			content = append(content, line(p.Muted, theme.StatusIcon("starting", m.tick, false)+" loading…"))
		case d.err != "":
			content = append(content, line(p.Red, "✖ "+d.err))
		default:
			opts := d.filtered()
			const visible = 8
			start := max(0, min(d.idx-visible/2, len(opts)-visible))
			for i := start; i < len(opts) && i < start+visible; i++ {
				text := theme.Fit(opts[i].Label, inner-2)
				if i == d.idx {
					content = append(content, newLine(p.Selection).add(p.Accent, "▌").add(p.Text, text).pad(inner).String())
				} else {
					content = append(content, newLine(p.PanelAlt).add(p.PanelAlt, " ").add(p.Text, text).String())
				}
			}
			if len(opts) == 0 {
				content = append(content, line(p.Dim, "no match"))
			}
		}
		content = append(content, "", line(p.Dim, "↑↓ move · enter choose · esc cancel"))
	}
	h := len(content) + 2
	x, y := center(W, H, w, h)
	return modalBox(title, w, h, content), w, x, y
}

func (m *Model) pluginCommands() []command {
	vd := m.viewDef()
	var out []command
	for i := range m.plug.actions {
		a := m.plug.actions[i]
		label := titleCase(a.plugin) + ": " + a.def.Title
		switch {
		case a.def.Scope.Service:
			for _, n := range m.names() {
				n := n
				out = append(out, command{id: "plugin-" + a.plugin + "-" + a.def.ID + "-" + n, label: label + " · " + n, hint: a.key,
					run: func(m *Model) tea.Cmd { return m.pluginRun(&a, n) }})
			}
		case a.def.Scope.View != "" && (vd.plugin == nil || vd.plugin.plugin != a.plugin || vd.plugin.def.ID != a.def.Scope.View):
			// it only makes sense in its view
		default:
			out = append(out, command{id: "plugin-" + a.plugin + "-" + a.def.ID, label: label, hint: a.key,
				run: func(m *Model) tea.Cmd { return m.pluginRun(&a, m.selected) }})
		}
	}
	return out
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (m *Model) pluginHelpRows() [][2]string {
	var rows [][2]string
	for i, v := range m.allViews() {
		if v.plugin != nil {
			rows = append(rows, [2]string{fmt.Sprint(i + 1), v.label + " (plugin " + v.plugin.plugin + ")"})
		}
	}
	for _, a := range m.plug.actions {
		if a.key == "" {
			continue
		}
		where := "(" + a.plugin + ") "
		if a.def.Scope.View != "" {
			where = "(" + a.plugin + " " + a.def.Scope.View + ") "
		}
		rows = append(rows, [2]string{where + a.key, a.def.Title})
	}
	return rows
}

func (m *Model) pluginHints() [][2]string {
	vd := m.viewDef()
	hints := [][2]string{}
	pn := m.focusedPluginPane()
	switch {
	case pn != nil && pn.def.Widget == protocol.WidgetList:
		hints = append(hints, [2]string{"j/k", "move"}, [2]string{"/", "filter"})
	case pn != nil:
		hints = append(hints, [2]string{"j/k", "scroll"})
	}
	hints = append(hints, [2]string{"tab", "focus"}, [2]string{"z", "zoom"})
	for _, a := range m.plug.actions {
		if a.key == "" || a.plugin != vd.plugin.plugin || (a.def.Scope.View != "" && a.def.Scope.View != vd.plugin.def.ID) {
			continue
		}
		if a.def.Scope.Pane != "" && (pn == nil || pn.def.ID != a.def.Scope.Pane) {
			continue
		}
		hints = append(hints, [2]string{a.key, strings.ToLower(a.def.Title)})
	}
	return append(hints, [2]string{"ctrl+r", "refresh"}, [2]string{":", "commands"}, [2]string{"?", "help"}, [2]string{"q", "quit"})
}
