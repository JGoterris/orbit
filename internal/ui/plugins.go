package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/internal/ui/theme"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

const pluginViewPrefix = "p:"

// pluginViewRef is the view of a plugin: what a viewDef carries when it is not one of orbit's own.
type pluginViewRef struct {
	plugin string
	def    protocol.View
}

func paneKey(plugin, view, pane string) string { return plugin + "/" + view + "/" + pane }

func pluginViewID(plugin, view string) string { return pluginViewPrefix + plugin + "/" + view }

// pluginPane is what the interface knows about a pane of a plugin: the last data the plugin sent and where the
// user is in it. The data belongs to the plugin; the selection and the scroll belong to this interface.
type pluginPane struct {
	key    string
	plugin string
	view   protocol.View
	def    protocol.Pane

	data protocol.ViewData
	// vis are the indexes of the items of a list that pass the filter
	vis    []int
	err    string
	loaded bool
	// loading: a request is in flight; stale: what is shown is known to be out of date
	loading bool
	stale   bool
	req     int

	sel    string // id of the selected item
	idx    int    // its position among vis
	scroll int    // detail and text: first row shown
	filter string
	follow bool // text: keeps the end in view

	// set by the last render: what the scroll keys need, the columns of text it had and, for a list, the row of
	// each line drawn
	total, page int
	width       int
	lineItems   []int
	// reqWidth is the width the data shown was asked for with
	reqWidth int
}

// pluginUI is what the interface keeps about the plugins of the project.
type pluginUI struct {
	infos  []plugins.Info
	badges map[string][]plugins.TaggedBadge
	panes  map[string]*pluginPane
	// sections are what plugins add to the detail of a service: "plugin/section/service"
	sections    map[string][]protocol.Section
	sectionsReq map[string]bool
	actions     []pluginAction
	overrides   map[string]string
	dialog      *pluginDialog
	filtering   *pluginPane
	filterBar   input
	// running is the title of the action that was sent and has not answered yet
	running string
	seq     int
	// ctxService is the service the last data was asked for
	ctxService string
	// item is the list whose selected item is open full screen (enter), with the panes that follow it
	item string
	// failed are the plugins whose failure was already reported
	failed map[string]bool
}

type pluginAction struct {
	plugin string
	def    protocol.Action
	key    string
}

// reservedKeys are the keys that orbit and the widgets of the plugin views keep for themselves: the action of a
// view cannot take them.
var reservedKeys = map[string]bool{
	"q": true, "ctrl+c": true, "ctrl+p": true, ":": true, "?": true, "T": true, "!": true, "U": true, "P": true,
	"p": true, "tab": true, "shift+tab": true, "z": true, "+": true, "-": true, "=": true, "esc": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true,
	"j": true, "k": true, "g": true, "G": true, "up": true, "down": true, "home": true, "end": true,
	"pgup": true, "pgdown": true, "ctrl+d": true, "ctrl+u": true, "/": true, "ctrl+r": true, "[": true, "]": true,
}

// ResolvePluginKeys gives every action of the plugins its key: the manifest's, or the one the user set
// (`keys` of config.json, "none" removes it). An action loses its key when it is one orbit keeps or when another
// action of the same scope has it; problems say why, so `orbit plugin doctor` can tell.
func ResolvePluginKeys(infos []plugins.Info, overrides map[string]string) (actions []pluginAction, problems []string) {
	taken := map[string]string{}
	for _, info := range infos {
		if info.Missing {
			continue
		}
		for _, a := range info.Contributes.Actions {
			id := info.Name + "." + a.ID
			key := a.Key
			if o, ok := overrides[id]; ok {
				key = o
			}
			if key == "none" {
				key = ""
			}
			if key != "" {
				scope := fmt.Sprintf("%s|%s|%s", a.Scope.View, a.Scope.Pane, key)
				if a.Scope.View != "" {
					scope = fmt.Sprintf("%s/%s|%s|%s", info.Name, a.Scope.View, a.Scope.Pane, key)
				}
				switch {
				case reservedKeys[key]:
					problems = append(problems, fmt.Sprintf("%s: %q is a key of orbit", id, key))
					key = ""
				case taken[scope] != "":
					problems = append(problems, fmt.Sprintf("%s: %q is already used by %s", id, key, taken[scope]))
					key = ""
				default:
					taken[scope] = id
				}
			}
			actions = append(actions, pluginAction{plugin: info.Name, def: a, key: key})
		}
	}
	return actions, problems
}

func (m *Model) initPlugins() {
	m.plug = pluginUI{
		panes: map[string]*pluginPane{}, sections: map[string][]protocol.Section{}, sectionsReq: map[string]bool{},
		failed: map[string]bool{}, overrides: m.opts.PluginKeys,
	}
	m.pluginRefresh()
}

func (m *Model) pluginRefresh() {
	host := m.sup.Plugins()
	m.plug.infos = host.Plugins()
	m.plug.badges = host.Badges()
	m.plug.actions, _ = ResolvePluginKeys(m.plug.infos, m.plug.overrides)
	tags := map[string]string{}
	for _, info := range m.plug.infos {
		for _, t := range info.Contributes.ServiceTypes {
			tags[t.Name] = t.Badge
			if t.Badge == "" {
				tags[t.Name] = t.Name
			}
		}
	}
	theme.SetPluginBadges(tags)
}

func (m *Model) pluginInfo(name string) (plugins.Info, bool) {
	for _, i := range m.plug.infos {
		if i.Name == name {
			return i, true
		}
	}
	return plugins.Info{}, false
}

// allViews are the views of orbit and then the ones the plugins add, in the order of the tabs.
func (m *Model) allViews() []viewDef {
	out := slices.Clone(views)
	for _, info := range m.plug.infos {
		if info.Missing {
			continue
		}
		for _, v := range info.Contributes.Views {
			out = append(out, pluginViewDef(info.Name, v))
		}
	}
	return out
}

func pluginViewDef(plugin string, v protocol.View) viewDef {
	vd := viewDef{id: pluginViewID(plugin, v.ID), label: v.Title, plugin: &pluginViewRef{plugin: plugin, def: v}}
	if v.Sidebar == protocol.SidebarServices {
		vd.panes = append(vd.panes, paneServices)
		vd.compactSidebar = true
	}
	for _, p := range v.Panes {
		vd.panes = append(vd.panes, pane(pluginViewPrefix+paneKey(plugin, v.ID, p.ID)))
	}
	vd.defaultPane = vd.panes[len(vd.panes)-len(v.Panes)]
	return vd
}

func (m *Model) viewByID(id string) viewDef {
	for _, v := range m.allViews() {
		if v.id == id {
			return v
		}
	}
	return views[0]
}

func (m *Model) pluginPaneOf(vd viewDef, def protocol.Pane) *pluginPane {
	key := paneKey(vd.plugin.plugin, vd.plugin.def.ID, def.ID)
	pn := m.plug.panes[key]
	if pn == nil {
		pn = &pluginPane{key: key, plugin: vd.plugin.plugin, view: vd.plugin.def, def: def, follow: def.Follow}
		m.plug.panes[key] = pn
	}
	pn.def = def
	return pn
}

func (m *Model) focusedPluginPane() *pluginPane {
	vd := m.viewDef()
	if vd.plugin == nil {
		return nil
	}
	for _, d := range vd.plugin.def.Panes {
		if m.focus == pane(pluginViewPrefix+paneKey(vd.plugin.plugin, vd.plugin.def.ID, d.ID)) {
			return m.pluginPaneOf(vd, d)
		}
	}
	return nil
}

func (m *Model) pluginContext(vd viewDef) protocol.Context {
	c := protocol.Context{Service: m.selected}
	if vd.plugin == nil {
		return c
	}
	c.Selection = map[string]string{}
	for _, d := range vd.plugin.def.Panes {
		if pn := m.plug.panes[paneKey(vd.plugin.plugin, vd.plugin.def.ID, d.ID)]; pn != nil {
			if pn.sel != "" {
				c.Selection[d.ID] = pn.sel
			}
			if pane(pluginViewPrefix+pn.key) == m.focus {
				c.Filter = pn.filter
			}
		}
	}
	return c
}

type pluginDataMsg struct {
	key  string
	req  int
	data protocol.ViewData
	err  error
}

type pluginSectionsMsg struct {
	key      string
	sections []protocol.Section
	err      error
}

type pluginUpdateMsg struct{ u plugins.Update }

func (m *Model) pluginLoadPane(vd viewDef, def protocol.Pane) tea.Cmd {
	pn := m.pluginPaneOf(vd, def)
	m.plug.seq++
	pn.req, pn.loading, pn.stale = m.plug.seq, true, false
	req, plugin, view := pn.req, pn.plugin, pn.view.ID
	ctx := m.pluginContext(vd)
	ctx.Width = pn.width
	pn.reqWidth = pn.width
	host := m.sup.Plugins()
	return func() tea.Msg {
		d, err := host.Data(plugin, protocol.ViewDataParams{View: view, Pane: def.ID, Context: ctx})
		return pluginDataMsg{key: pn.key, req: req, data: d, err: err}
	}
}

// pluginLoadView asks for the panes of the view that are not loaded or are stale. A pane that follows another
// (bind) waits for that one to have a selection.
func (m *Model) pluginLoadView(vd viewDef, all bool) tea.Cmd {
	if vd.plugin == nil {
		return nil
	}
	if info, ok := m.pluginInfo(vd.plugin.plugin); !ok || info.Status != plugins.StatusRunning {
		return nil
	}
	var cmds []tea.Cmd
	for _, def := range vd.plugin.def.Panes {
		pn := m.pluginPaneOf(vd, def)
		if !all && (pn.loading || (pn.loaded && !pn.stale)) {
			continue
		}
		if m.pluginBindMet(vd, def) {
			cmds = append(cmds, m.pluginLoadPane(vd, def))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) onPluginData(msg pluginDataMsg) tea.Cmd {
	pn := m.plug.panes[msg.key]
	if pn == nil || pn.req != msg.req {
		return nil // a newer request was made since
	}
	pn.loading, pn.loaded = false, true
	if msg.err != nil {
		pn.err = msg.err.Error()
		return nil
	}
	pn.err = ""
	pn.data = msg.data
	if pn.refresh() {
		return m.pluginSelectionChanged(pn)
	}
	return nil
}

func (m *Model) pluginSelectionChanged(pn *pluginPane) tea.Cmd {
	vd := m.viewByID(pluginViewID(pn.plugin, pn.view.ID))
	if vd.plugin == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, def := range pn.view.Panes {
		if def.Bind != pn.def.ID {
			continue
		}
		bound := m.pluginPaneOf(vd, def)
		bound.scroll = 0
		bound.follow = def.Follow
		// what it shows is about the item that was selected: it says it is loading rather than show that
		bound.loaded, bound.err = false, ""
		if m.view == vd.id {
			cmds = append(cmds, m.pluginLoadPane(vd, def))
		} else {
			bound.stale = true
		}
	}
	return tea.Batch(cmds...)
}

// refresh recomputes what a list shows after its data or its filter changed, keeping the selection on the same
// item when it is still there. It reports whether the selected item is another one now.
func (pn *pluginPane) refresh() (selectionChanged bool) {
	before := pn.sel
	if pn.data.List != nil {
		pn.vis = pn.vis[:0]
		for i, it := range pn.data.List.Items {
			if pn.filter == "" {
				pn.vis = append(pn.vis, i)
				continue
			}
			if _, ok := fuzzyScore(pn.filter, itemText(pn.data.List, it)); ok {
				pn.vis = append(pn.vis, i)
			}
		}
		at := -1
		for j, i := range pn.vis {
			if pn.data.List.Items[i].ID == pn.sel {
				at = j
			}
		}
		if at < 0 {
			at = max(0, min(pn.idx, len(pn.vis)-1))
		}
		pn.idx = at
		pn.sel = ""
		if len(pn.vis) > 0 {
			pn.sel = pn.data.List.Items[pn.vis[at]].ID
		}
	}
	return pn.sel != before
}

func itemText(d *protocol.ListData, it protocol.Item) string {
	var parts []string
	for _, c := range d.Columns {
		if cell, ok := it.Cells[c.ID]; ok {
			parts = append(parts, cell.Text)
		}
	}
	if len(d.Columns) == 0 {
		for _, c := range it.Cells {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, " ")
}

func (pn *pluginPane) selectedItem() (protocol.Item, bool) {
	if pn.data.List == nil || pn.idx < 0 || pn.idx >= len(pn.vis) {
		return protocol.Item{}, false
	}
	return pn.data.List.Items[pn.vis[pn.idx]], true
}

func (pn *pluginPane) move(to int) bool {
	if len(pn.vis) == 0 {
		return false
	}
	to = max(0, min(len(pn.vis)-1, to))
	pn.idx = to
	id := pn.data.List.Items[pn.vis[to]].ID
	changed := id != pn.sel
	pn.sel = id
	return changed
}

func (pn *pluginPane) scrollTo(to int) {
	maxScroll := max(0, pn.total-pn.page)
	pn.scroll = max(0, min(maxScroll, to))
	pn.follow = pn.def.Follow && pn.scroll >= maxScroll
}

// pluginItemText is the name the selected item of a pane goes by in a question ("Merge !41?"): of the list the pane
// follows, when it is not a list.
func (m *Model) pluginItemText(pn *pluginPane) string {
	if src := m.pluginSourceOf(pn); src != nil && pn.def.Widget != protocol.WidgetList {
		pn = src
	}
	it, ok := pn.selectedItem()
	if !ok {
		return ""
	}
	if pn.data.List != nil && len(pn.data.List.Columns) > 0 {
		if c, ok := it.Cells[pn.data.List.Columns[0].ID]; ok && c.Text != "" {
			return c.Text
		}
	}
	return it.ID
}

func (m *Model) onPluginUpdate(u plugins.Update) tea.Cmd {
	p := theme.Active()
	switch u.Kind {
	case plugins.UpdateToast:
		if u.Toast != nil {
			return m.notify(u.Toast.Text, m.toneColor(u.Toast.Tone))
		}
	case plugins.UpdateBadges:
		m.pluginRefresh()
	case plugins.UpdateStatus:
		m.pluginRefresh()
		if info, ok := m.pluginInfo(u.Plugin); ok {
			switch {
			case info.Status == plugins.StatusFailed && !m.plug.failed[u.Plugin]:
				m.plug.failed[u.Plugin] = true
				return m.notify(fmt.Sprintf("plugin %s failed: %s (orbit plugin logs %s)", u.Plugin, firstLine(info.Error), u.Plugin), p.Red)
			case info.Status == plugins.StatusRunning:
				m.plug.failed[u.Plugin] = false
				// it came back (or just started): what it showed before may be gone
				return m.pluginLoadView(m.viewDef(), true)
			}
		}
	case plugins.UpdateInvalidate:
		return m.onPluginInvalidate(u)
	}
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// onPluginInvalidate reloads what a plugin said is stale: at once if it is on screen, when it is opened if not.
func (m *Model) onPluginInvalidate(u plugins.Update) tea.Cmd {
	inv := protocol.Invalidate{}
	if u.Invalidate != nil {
		inv = *u.Invalidate
	}
	for key := range m.plug.sections {
		if strings.HasPrefix(key, u.Plugin+"/") {
			delete(m.plug.sections, key)
		}
	}
	vd := m.viewDef()
	var cmds []tea.Cmd
	for _, pn := range m.plug.panes {
		if pn.plugin != u.Plugin || (inv.View != "" && inv.View != pn.view.ID) || (inv.Pane != "" && inv.Pane != pn.def.ID) {
			continue
		}
		if vd.plugin == nil || vd.plugin.plugin != pn.plugin || vd.plugin.def.ID != pn.view.ID {
			pn.stale = true
			continue
		}
		if m.pluginBindMet(vd, pn.def) {
			cmds = append(cmds, m.pluginLoadPane(vd, pn.def))
		}
	}
	cmds = append(cmds, m.pluginLoadSections())
	return tea.Batch(cmds...)
}

// pluginResized asks again for the panes on screen that draw according to their width (sized) and were drawn at
// another width than the one their data was asked for: the terminal, the zoom or the layout changed.
func (m *Model) pluginResized() tea.Cmd {
	vd := m.viewDef()
	if vd.plugin == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, def := range vd.plugin.def.Panes {
		pn := m.plug.panes[paneKey(vd.plugin.plugin, vd.plugin.def.ID, def.ID)]
		if pn == nil || !def.Sized || !pn.loaded || pn.loading || pn.width == 0 || pn.width == pn.reqWidth {
			continue
		}
		if m.pluginBindMet(vd, def) {
			cmds = append(cmds, m.pluginLoadPane(vd, def))
		}
	}
	return tea.Batch(cmds...)
}

// pluginBoundPanes are the panes of the view that follow the selection of pane src.
func pluginBoundPanes(v protocol.View, src string) []protocol.Pane {
	var out []protocol.Pane
	for _, d := range v.Panes {
		if d.Bind == src && src != "" {
			out = append(out, d)
		}
	}
	return out
}

// pluginSourceOf is the list the pane follows, when it follows one.
func (m *Model) pluginSourceOf(pn *pluginPane) *pluginPane {
	if pn == nil || pn.def.Bind == "" {
		return nil
	}
	src := m.plug.panes[paneKey(pn.plugin, pn.view.ID, pn.def.Bind)]
	if src == nil || src.def.Widget != protocol.WidgetList {
		return nil
	}
	return src
}

// pluginItemView is the list whose selected item is open full screen in the view on screen, or nil.
func (m *Model) pluginItemView() *pluginPane {
	if !m.zoomed || m.plug.item == "" {
		return nil
	}
	src := m.pluginSourceOf(m.focusedPluginPane())
	if src == nil || src.key != m.plug.item {
		return nil
	}
	return src
}

// pluginOpenItem shows the selected item of a list full screen: the panes that follow the list, one over the other,
// with the whole width.
func (m *Model) pluginOpenItem(pn *pluginPane) bool {
	bound := pluginBoundPanes(pn.view, pn.def.ID)
	if len(bound) == 0 || pn.sel == "" {
		return false
	}
	m.plug.item = pn.key
	m.zoomed = true
	m.focus = pane(pluginViewPrefix + paneKey(pn.plugin, pn.view.ID, bound[0].ID))
	return true
}

// pluginCloseItem goes back from an item shown full screen to its list.
func (m *Model) pluginCloseItem() bool {
	src := m.pluginItemView()
	m.plug.item = ""
	if src == nil {
		return false
	}
	m.zoomed = false
	m.focus = pane(pluginViewPrefix + src.key)
	return true
}

func (m *Model) pluginBindMet(vd viewDef, def protocol.Pane) bool {
	if def.Bind == "" {
		return true
	}
	src := m.plug.panes[paneKey(vd.plugin.plugin, vd.plugin.def.ID, def.Bind)]
	return src != nil && src.sel != ""
}

// pluginOnSelection is called whenever the view or the selected service may have changed: it loads what the
// screen needs from the plugins.
func (m *Model) pluginOnSelection() tea.Cmd {
	if len(m.plug.infos) == 0 {
		return nil
	}
	vd := m.viewDef()
	var cmds []tea.Cmd
	if vd.plugin != nil {
		all := vd.plugin.def.Sidebar == protocol.SidebarServices && m.plug.ctxService != m.selected
		cmds = append(cmds, m.pluginLoadView(vd, all))
	}
	m.plug.ctxService = m.selected
	cmds = append(cmds, m.pluginLoadSections())
	return tea.Batch(cmds...)
}

func sectionKey(plugin, section, service string) string {
	return plugin + "/" + section + "/" + service
}

// pluginLoadSections asks for the blocks the plugins add to the detail of the selected service, in the views that
// show it.
func (m *Model) pluginLoadSections() tea.Cmd {
	if m.selected == "" || !m.viewDef().has(paneDetail) {
		return nil
	}
	host := m.sup.Plugins()
	var cmds []tea.Cmd
	for _, info := range m.plug.infos {
		if info.Missing || info.Status != plugins.StatusRunning {
			continue
		}
		for _, s := range info.Contributes.ServiceSections {
			key := sectionKey(info.Name, s.ID, m.selected)
			if _, have := m.plug.sections[key]; have || m.plug.sectionsReq[key] {
				continue
			}
			m.plug.sectionsReq[key] = true
			plugin, section, service := info.Name, s.ID, m.selected
			cmds = append(cmds, func() tea.Msg {
				out, err := host.Sections(plugin, protocol.SectionsParams{Service: service, Section: section})
				return pluginSectionsMsg{key: key, sections: out, err: err}
			})
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) onPluginSections(msg pluginSectionsMsg) {
	delete(m.plug.sectionsReq, msg.key)
	if msg.err != nil {
		m.plug.sections[msg.key] = nil
		return
	}
	m.plug.sections[msg.key] = msg.sections
}

func (m *Model) pluginSectionsOf(service string) []protocol.Section {
	var out []protocol.Section
	for _, info := range m.plug.infos {
		for _, s := range info.Contributes.ServiceSections {
			for _, sec := range m.plug.sections[sectionKey(info.Name, s.ID, service)] {
				if sec.Title == "" {
					sec.Title = s.Title
				}
				out = append(out, sec)
			}
		}
	}
	return out
}

func (m *Model) pluginServiceBadges(service string) []plugins.TaggedBadge {
	return m.plug.badges["service:"+service]
}

func (m *Model) toneColor(t protocol.Tone) string {
	p := theme.Active()
	switch t {
	case protocol.ToneOK:
		return p.Green
	case protocol.ToneWarn:
		return p.Yellow
	case protocol.ToneError:
		return p.Red
	case protocol.ToneInfo:
		return p.Cyan
	case protocol.ToneMuted:
		return p.Muted
	case protocol.ToneAccent:
		return p.Accent
	}
	if name, ok := strings.CutPrefix(string(t), "service:"); ok {
		return theme.ServiceColor(name, m.sup.Names())
	}
	return p.Text
}

func (m *Model) pluginIcon(name string) (glyph, color string) { return iconOf(name, m.tick) }

func iconOf(name string, tick int) (glyph, color string) {
	p := theme.Active()
	switch name {
	case "ok":
		return "✓", p.Green
	case "fail":
		return "✖", p.Red
	case "run":
		return theme.StatusIcon("starting", tick, false), p.Yellow
	case "wait":
		return "◌", p.Dim
	case "skip":
		return "⊘", p.Muted
	case "off":
		return "○", p.Dim
	case "warn":
		return "▲", p.Orange
	case "info":
		return "●", p.Cyan
	case "open":
		return "●", p.Green
	case "merge":
		return "◆", p.Accent2
	case "closed":
		return "○", p.Muted
	case "draft":
		return "◌", p.Muted
	}
	return "", ""
}
