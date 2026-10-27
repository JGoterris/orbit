package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/internal/ui/theme"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// minPluginWidth is the width under which a plugin view shows one pane at a time, as zoomed.
const minPluginWidth = 70

// renderPluginView draws the panes of a plugin view in the area to the right of the sidebar (or the whole body
// when the view has none): the first pane on the left, and the rest stacked on its right.
func (m *Model) renderPluginView(vd viewDef, W, H, x int) []string {
	p := theme.Active()
	info, ok := m.pluginInfo(vd.plugin.plugin)
	if !ok || info.Status != plugins.StatusRunning {
		return m.renderPluginDown(vd, info, W, H)
	}
	defs := vd.plugin.def.Panes
	focused := m.focusedPluginPane()
	if m.zoomed || W < minPluginWidth {
		if focused == nil {
			focused = m.pluginPaneOf(vd, defs[0])
		}
		defs = []protocol.Pane{focused.def}
	}
	if len(defs) == 1 {
		return m.renderPluginPane(vd, defs[0], W, H, x, 1)
	}
	weight := func(d protocol.Pane) int { return max(1, d.Weight) }
	rest := defs[1:]
	rightWeight := 1
	for _, d := range rest {
		rightWeight = max(rightWeight, weight(d))
	}
	w0 := weight(defs[0])
	leftW := max(20, min(W-20, W*w0/(w0+rightWeight)))
	left := m.renderPluginPane(vd, defs[0], leftW, H, x, 1)

	ws := make([]int, len(rest))
	for i, d := range rest {
		ws[i] = weight(d)
	}
	heights := distribute(H, ws, 3)
	var right []string
	y := 1
	for i, d := range rest {
		right = append(right, m.renderPluginPane(vd, d, W-leftW, heights[i], x+leftW, y)...)
		y += heights[i]
	}
	if len(right) < H {
		right = append(right, fill(W-leftW, H-len(right), p.Bg)...)
	}
	return hconcat(left, right)
}

func (m *Model) renderPluginDown(vd viewDef, info plugins.Info, W, H int) []string {
	p := theme.Active()
	name := vd.plugin.plugin
	l := newLine(p.Panel)
	var content []string
	switch info.Status {
	case plugins.StatusFailed:
		l.bold(p.Red, "✖ plugin "+name+" is not running")
		content = append(content, "", l.String())
		for _, ln := range wrapText(info.Error, max(10, W-6)) {
			content = append(content, newLine(p.Panel).add(p.Muted, ln).String())
		}
		content = append(content, "", newLine(p.Panel).add(p.Dim, "its log: orbit plugin logs "+name).String())
	case plugins.StatusStopped:
		content = []string{"", l.add(p.Muted, "plugin "+name+" is stopped").String()}
	default:
		content = []string{"", l.add(p.Yellow, theme.StatusIcon("starting", m.tick, false)+" starting plugin "+name+"…").String()}
	}
	return panel(panelOpts{title: vd.label, focused: true, padX: 1}, W, H, content)
}

func wrapText(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		cur := ""
		for _, w := range words {
			switch {
			case cur == "":
				cur = w
			case ansi.StringWidth(cur)+1+ansi.StringWidth(w) <= width:
				cur += " " + w
			default:
				out = append(out, cur)
				cur = w
			}
			for ansi.StringWidth(cur) > width {
				out = append(out, ansi.Truncate(cur, width, ""))
				cur = ansi.Cut(cur, width, ansi.StringWidth(cur))
			}
		}
		out = append(out, cur)
	}
	return out
}

func (m *Model) renderPluginPane(vd viewDef, def protocol.Pane, w, h, x, y int) []string {
	p := theme.Active()
	pn := m.pluginPaneOf(vd, def)
	id := pane(pluginViewPrefix + pn.key)
	focused := m.focus == id
	title := def.Title
	if title == "" {
		title = def.ID
	}
	if pn.def.Widget == protocol.WidgetList && pn.loaded && pn.data.List != nil {
		title += fmt.Sprintf(" · %d", len(pn.vis))
		if pn.filter != "" {
			title += " · /" + pn.filter
		}
	}
	if pn.loading && pn.loaded {
		title += " " + theme.StatusIcon("starting", m.tick, false)
	}
	bottom := ""
	if pn.err != "" && pn.loaded {
		bottom = "✖ " + theme.Fit(firstLine(pn.err), max(8, w/2))
	}

	if msg, color := m.pluginPaneMessage(vd, pn); msg != "" {
		m.hit(id, x, y, w, h, 0, 0)
		lines := []string{""}
		for _, ln := range wrapText(msg, max(10, w-6)) {
			lines = append(lines, newLine(p.Panel).add(color, ln).String())
		}
		return panel(panelOpts{title: title, focused: focused, padX: 1}, w, h, lines)
	}

	switch def.Widget {
	case protocol.WidgetList:
		rows := m.pluginListRows(pn, w-3)
		first := listStart(len(rows), pn.idx, h)
		m.hit(id, x, y, w, h, len(rows), first)
		foot := bottom
		if foot == "" {
			foot = theme.Fit(pn.data.List.Footer, max(0, w-6))
			foot = strings.TrimRight(foot, " ")
		}
		return listPanel(title, rows, pn.idx, focused, m.pluginEmpty(pn.data.List.Empty), foot, w, h)
	case protocol.WidgetDetail:
		return m.scrollPluginPane(pn, id, title, bottom, m.pluginDetailLines(pn.data.Detail, w-4), focused, w, h, x, y)
	default:
		return m.scrollPluginPane(pn, id, title, bottom, m.pluginTextLines(pn.data.Text), focused, w, h, x, y)
	}
}

func (m *Model) pluginEmpty(s string) string {
	if s == "" {
		return "nothing here"
	}
	return s
}

// pluginPaneMessage is what a pane says instead of its content: it is loading, it has nothing to follow, it failed
// before it ever got data.
func (m *Model) pluginPaneMessage(vd viewDef, pn *pluginPane) (msg, color string) {
	p := theme.Active()
	switch {
	case pn.def.Bind != "" && !m.pluginBindMet(vd, pn.def):
		return "nothing selected", p.Dim
	case !pn.loaded:
		return theme.StatusIcon("starting", m.tick, false) + " loading…", p.Muted
	case pn.err != "" && pn.data.List == nil && pn.data.Detail == nil && pn.data.Text == nil:
		return "✖ " + pn.err, p.Red
	}
	switch pn.def.Widget {
	case protocol.WidgetList:
		if pn.data.List == nil {
			return "the plugin sent no list for this pane", p.Orange
		}
	case protocol.WidgetDetail:
		if pn.data.Detail == nil {
			return "the plugin sent no detail for this pane", p.Orange
		}
		if len(pn.data.Detail.Sections) == 0 {
			return m.pluginEmpty(pn.data.Detail.Empty), p.Dim
		}
	default:
		if pn.data.Text == nil {
			return "the plugin sent no text for this pane", p.Orange
		}
		if len(pn.data.Text.Lines) == 0 {
			return m.pluginEmpty(pn.data.Text.Empty), p.Dim
		}
	}
	return "", ""
}

func (m *Model) scrollPluginPane(pn *pluginPane, id pane, title, bottom string, lines []string, focused bool, w, h, x, y int) []string {
	pn.total, pn.page = len(lines), max(1, h-2)
	if pn.follow {
		pn.scroll = max(0, pn.total-pn.page)
	}
	pn.scroll = max(0, min(pn.scroll, max(0, pn.total-pn.page)))
	if bottom == "" && pn.total > pn.page {
		bottom = fmt.Sprintf("%d-%d/%d", pn.scroll+1, min(pn.total, pn.scroll+pn.page), pn.total)
	}
	m.hit(id, x, y, w, h, 0, 0)
	end := min(pn.total, pn.scroll+pn.page)
	return panel(panelOpts{title: title, bottom: bottom, focused: focused, padX: 1}, w, h, lines[pn.scroll:end])
}

type pluginCol struct {
	id string
	w  int
}

// pluginColumns decides which columns of a list fit in avail columns. The first takes what the others leave; the
// ones with the highest priority number go first when it does not fit.
func pluginColumns(d *protocol.ListData, avail int, hasIcon bool) (primary pluginCol, others []pluginCol, badgeW int) {
	cols := slices.Clone(d.Columns)
	if len(cols) == 0 && len(d.Items) > 0 {
		var ids []string
		for id := range d.Items[0].Cells {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			cols = append(cols, protocol.Column{ID: id})
		}
	}
	if len(cols) == 0 {
		return pluginCol{}, nil, 0
	}
	natural := func(c protocol.Column) int {
		if c.Width > 0 {
			return c.Width
		}
		w := 1
		for _, it := range d.Items {
			w = max(w, ansi.StringWidth(it.Cells[c.ID].Text))
		}
		return min(24, w)
	}
	for _, it := range d.Items {
		bw := 0
		for _, b := range it.Badges {
			bw += ansi.StringWidth(b.Text) + 1
			if g, _ := iconOf(b.Icon, 0); g != "" {
				bw += 2
			}
		}
		badgeW = max(badgeW, min(bw, 18))
	}
	if hasIcon {
		avail -= 2
	}
	const minPrimary = 10
	type cand struct {
		col  pluginCol
		prio int
		idx  int
	}
	var cs []cand
	for i, c := range cols[1:] {
		cs = append(cs, cand{pluginCol{c.ID, natural(c)}, c.Priority, i})
	}
	used := func() int {
		n := 0
		for _, c := range cs {
			n += c.col.w + 1
		}
		if badgeW > 0 {
			n += badgeW + 1
		}
		return n
	}
	for len(cs) > 0 || badgeW > 0 {
		if avail-used() >= min(minPrimary, natural(cols[0])) {
			break
		}
		if badgeW > 0 && len(cs) == 0 {
			badgeW = 0
			continue
		}
		worst := 0
		for i, c := range cs {
			if c.prio > cs[worst].prio || (c.prio == cs[worst].prio && c.idx > cs[worst].idx) {
				worst = i
			}
		}
		cs = slices.Delete(cs, worst, worst+1)
	}
	for _, c := range cs {
		others = append(others, c.col)
	}
	return pluginCol{cols[0].ID, max(1, avail-used())}, others, badgeW
}

func (m *Model) pluginListRows(pn *pluginPane, avail int) []listRow {
	p := theme.Active()
	d := pn.data.List
	hasIcon := false
	for _, it := range d.Items {
		if it.Icon != "" {
			hasIcon = true
			break
		}
	}
	primary, others, badgeW := pluginColumns(d, avail, hasIcon)
	rows := make([]listRow, 0, len(pn.vis))
	for _, i := range pn.vis {
		it := d.Items[i]
		rows = append(rows, func(bg string) *line {
			l := newLine(bg)
			if hasIcon {
				if g, c := m.pluginIcon(it.Icon); g != "" {
					l.add(c, g+" ")
				} else {
					l.add(p.Dim, "  ")
				}
			}
			cell := it.Cells[primary.id]
			color := m.toneColor(cell.Tone)
			l.add(color, theme.Fit(cell.Text, primary.w))
			for _, c := range others {
				cell := it.Cells[c.id]
				col := m.toneColor(cell.Tone)
				if cell.Tone == "" {
					col = p.Muted
				}
				l.add(p.Text, " ").add(col, theme.Fit(cell.Text, c.w))
			}
			if badgeW > 0 {
				b := m.badgeLine(bg, it.Badges, badgeW)
				l.add(p.Text, " ").raw(b.String(), b.width())
			}
			return l
		})
	}
	return rows
}

func (m *Model) badgeLine(bg string, badges []protocol.Badge, width int) *line {
	l := newLine(bg)
	for _, b := range badges {
		part := newLine(bg)
		if g, c := m.pluginIcon(b.Icon); g != "" {
			part.add(c, g+" ")
		}
		part.add(m.toneColor(b.Tone), b.Text)
		if l.width()+part.width()+1 > width && l.width() > 0 {
			break
		}
		if l.width() > 0 {
			l.add("", " ")
		}
		l.raw(part.String(), part.width())
	}
	if l.width() > width {
		// even the first badge is wider than the column: cut it, a row must keep its width
		return newLine(bg).raw(ansi.Truncate(l.String(), width, "…"), width)
	}
	return l.pad(width)
}

func spansLine(m *Model, bg string, spans []protocol.Span) *line {
	l := newLine(bg)
	for _, s := range spans {
		color := m.toneColor(s.Tone)
		if s.Bold {
			l.bold(color, s.Text)
		} else {
			l.add(color, s.Text)
		}
	}
	return l
}

func (m *Model) pluginTextLines(t *protocol.TextData) []string {
	p := theme.Active()
	out := make([]string, 0, len(t.Lines))
	for _, spans := range t.Lines {
		out = append(out, spansLine(m, p.Panel, spans).String())
	}
	return out
}

// pluginDetailLines are the rows of a detail: for each section its title, its fields packed in lines, its text
// and its badges.
func (m *Model) pluginDetailLines(d *protocol.DetailData, inner int) []string {
	return m.pluginSectionLines(d.Sections, inner, 0)
}

func (m *Model) pluginSectionLines(sections []protocol.Section, inner, textMax int) []string {
	p := theme.Active()
	var out []string
	for i, s := range sections {
		if i > 0 {
			out = append(out, blank(inner, p.Panel))
		}
		if s.Title != "" {
			out = append(out, m.headerLine(s.Title, inner))
		}
		l := newLine(p.Panel)
		for _, f := range s.Fields {
			value := strings.TrimRight(theme.Fit(f.Value, max(4, min(ansi.StringWidth(f.Value), inner-len(f.Label)-4))), " ")
			need := len(f.Label) + ansi.StringWidth(value) + 4
			if l.width() > 0 && l.width()+need > inner {
				out = append(out, l.String())
				l = newLine(p.Panel)
			}
			color := m.toneColor(f.Tone)
			if f.Tone == "" {
				color = p.Text
			}
			field(l, f.Label, value, color)
		}
		if l.width() > 0 {
			out = append(out, l.String())
		}
		text := s.Text
		if textMax > 0 && len(text) > textMax {
			text = text[:textMax]
		}
		for _, spans := range text {
			out = append(out, spansLine(m, p.Panel, spans).String())
		}
		if len(s.Badges) > 0 {
			out = append(out, m.badgeLine(p.Panel, s.Badges, inner).String())
		}
	}
	return out
}

func (m *Model) pluginDetailExtra(service string, inner int) int {
	secs := m.pluginSectionsOf(service)
	if len(secs) == 0 {
		return 0
	}
	return min(maxPluginDetailRows, len(m.pluginSectionLines(secs, inner, 3)))
}

// maxPluginDetailRows keeps the plugins from taking the whole dashboard.
const maxPluginDetailRows = 8

func (m *Model) pluginServiceColumn(plugin, service string, w int, bg string) *line {
	var badges []protocol.Badge
	for _, b := range m.pluginServiceBadges(service) {
		if b.Plugin == plugin {
			badges = append(badges, b.Badge)
		}
	}
	return m.badgeLine(bg, badges, w)
}

func (m *Model) pluginServiceColumns() []pluginCol {
	var out []pluginCol
	for _, info := range m.plug.infos {
		if c := info.Contributes.ServiceColumn; c != nil && !info.Missing {
			out = append(out, pluginCol{id: info.Name, w: max(3, c.Width)})
		}
	}
	return out
}

type badgeHit struct {
	x0, x1 int
	plugin string
	action string
}

// headerPluginLine draws what the plugins show at the right of the header: their badges, and a mark for the ones
// that failed or that the project needs and are missing.
func (m *Model) headerPluginLine(bg string) (*line, []badgeHit) {
	p := theme.Active()
	l := newLine(bg)
	var hits []badgeHit
	for _, tb := range m.plug.badges["header"] {
		part := newLine(bg)
		if g, c := m.pluginIcon(tb.Icon); g != "" {
			part.add(c, g+" ")
		}
		part.add(m.toneColor(tb.Tone), tb.Text)
		if tb.Action != "" {
			hits = append(hits, badgeHit{x0: l.width(), x1: l.width() + part.width(), plugin: tb.Plugin, action: tb.Action})
		}
		l.raw(part.String(), part.width()).add(p.Dim, "  ")
	}
	for _, info := range m.plug.infos {
		switch {
		case info.Missing:
			l.add(p.Orange, "⚠ plugin "+info.Name+" missing  ")
		case info.Status == plugins.StatusFailed:
			l.add(p.Red, "✖ plugin "+info.Name+"  ")
		}
	}
	return l, hits
}
