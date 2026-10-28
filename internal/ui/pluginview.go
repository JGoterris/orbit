package ui

import (
	"cmp"
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
	if src := m.pluginItemView(); src != nil {
		return m.renderPluginItem(vd, src, W, H, x)
	}
	if m.zoomed || W < minPluginWidth {
		if focused == nil {
			focused = m.pluginPaneOf(vd, defs[0])
		}
		defs = []protocol.Pane{focused.def}
	}
	if len(defs) == 1 {
		return m.renderPluginPane(vd, defs[0], "", W, H, x, 1)
	}
	weight := func(d protocol.Pane) int { return max(1, d.Weight) }
	rest := defs[1:]
	rightWeight := 1
	for _, d := range rest {
		rightWeight = max(rightWeight, weight(d))
	}
	w0 := weight(defs[0])
	leftW := max(20, min(W-20, W*w0/(w0+rightWeight)))
	left := m.renderPluginPane(vd, defs[0], "", leftW, H, x, 1)

	ws := make([]int, len(rest))
	for i, d := range rest {
		ws[i] = weight(d)
	}
	heights := distribute(H, ws, 3)
	var right []string
	y := 1
	for i, d := range rest {
		right = append(right, m.renderPluginPane(vd, d, "", W-leftW, heights[i], x+leftW, y)...)
		y += heights[i]
	}
	if len(right) < H {
		right = append(right, fill(W-leftW, H-len(right), p.Bg)...)
	}
	return hconcat(left, right)
}

// renderPluginItem is the selected item of a list full screen: the panes that follow the list, one over the other,
// with the whole width, the position of the item in the title of the first.
func (m *Model) renderPluginItem(vd viewDef, src *pluginPane, W, H, x int) []string {
	defs := pluginBoundPanes(src.view, src.def.ID)
	ws := make([]int, len(defs))
	for i, d := range defs {
		ws[i] = max(1, d.Weight)
	}
	heights := distribute(H, ws, 3)
	var out []string
	y := 1
	for i, d := range defs {
		note := ""
		if i == 0 {
			note = fmt.Sprintf(" · %d of %d", src.idx+1, len(src.vis))
		}
		out = append(out, m.renderPluginPane(vd, d, note, W, heights[i], x, y)...)
		y += heights[i]
	}
	return out
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

// wrapSpans breaks a line of spans into lines of at most width columns, at a space when it can. The lines after the
// first keep the indentation of the first (and of its bullet, "- " or "* " or "• " or "> "), so an item of a list
// still reads as one.
func wrapSpans(spans []protocol.Span, width int) [][]protocol.Span {
	total := 0
	for _, s := range spans {
		total += ansi.StringWidth(s.Text)
	}
	if width < 1 || total <= width {
		return [][]protocol.Span{spans}
	}
	// words are runs of spaces or of anything else, each made of the pieces of the spans it crosses
	type word struct {
		parts []protocol.Span
		w     int
		space bool
	}
	var words []word
	var text strings.Builder
	for _, s := range spans {
		text.WriteString(s.Text)
		for _, r := range s.Text {
			space := r == ' '
			if len(words) == 0 || words[len(words)-1].space != space {
				words = append(words, word{space: space})
			}
			wd := &words[len(words)-1]
			if n := len(wd.parts); n > 0 && wd.parts[n-1].Tone == s.Tone && wd.parts[n-1].Bold == s.Bold {
				wd.parts[n-1].Text += string(r)
			} else {
				wd.parts = append(wd.parts, protocol.Span{Text: string(r), Tone: s.Tone, Bold: s.Bold})
			}
			wd.w += ansi.StringWidth(string(r))
		}
	}
	indent := 0
	if len(words) > 0 && words[0].space {
		indent = words[0].w
	}
	for _, bullet := range []string{"- ", "* ", "• ", "> "} {
		if strings.HasPrefix(strings.TrimLeft(text.String(), " "), bullet) {
			indent += 2
			break
		}
	}
	indent = min(indent, width/2)

	var out [][]protocol.Span
	var cur []protocol.Span
	cw := 0
	add := func(s protocol.Span) {
		if n := len(cur); n > 0 && cur[n-1].Tone == s.Tone && cur[n-1].Bold == s.Bold {
			cur[n-1].Text += s.Text
		} else {
			cur = append(cur, s)
		}
		cw += ansi.StringWidth(s.Text)
	}
	flush := func() {
		for len(cur) > 0 {
			last := &cur[len(cur)-1]
			last.Text = strings.TrimRight(last.Text, " ")
			if last.Text != "" {
				break
			}
			cur = cur[:len(cur)-1]
		}
		out = append(out, cur)
		cur, cw = nil, 0
		if indent > 0 {
			cur = []protocol.Span{{Text: strings.Repeat(" ", indent)}}
			cw = indent
		}
	}
	for _, wd := range words {
		if wd.space {
			switch {
			case cw+wd.w > width:
				flush()
			case len(out) > 0 && cw == indent:
				// a continuation line does not start with the spaces it was broken at
			default:
				for _, p := range wd.parts {
					add(p)
				}
			}
			continue
		}
		// a word that fits in a line of its own starts one; one that does not fit anywhere starts where it is
		if cw+wd.w > width && cw > indent && wd.w <= width-indent {
			flush()
		}
		// a word wider than the line is broken after a / - _ or . that still fits (a path, a branch), else where
		// the line ends
		breakAt := -1
		if cw+wd.w > width {
			n, w := 0, cw
			for _, p := range wd.parts {
				for _, r := range p.Text {
					w += ansi.StringWidth(string(r))
					n++
					if w > width {
						break
					}
					if strings.ContainsRune("/-_.", r) && w > cw+(width-cw)/3 {
						breakAt = n
					}
				}
			}
		}
		n := 0
		for _, p := range wd.parts {
			for _, r := range p.Text {
				rw := ansi.StringWidth(string(r))
				if (n == breakAt || cw+rw > width) && cw > indent {
					flush()
					breakAt = -1
				}
				add(protocol.Span{Text: string(r), Tone: p.Tone, Bold: p.Bold})
				n++
			}
		}
	}
	if cw > indent || len(out) == 0 {
		out = append(out, cur)
	}
	return out
}

// renderPluginPane draws a pane of a plugin; note goes after its title.
func (m *Model) renderPluginPane(vd viewDef, def protocol.Pane, note string, w, h, x, y int) []string {
	p := theme.Active()
	pn := m.pluginPaneOf(vd, def)
	pn.width = w - 4
	id := pane(pluginViewPrefix + pn.key)
	focused := m.focus == id
	title := cmp.Or(def.Title, def.ID) + note
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
		rows, more := m.pluginListRows(pn, w-3)
		m.hit(id, x, y, w, h, len(rows), 0)
		foot := bottom
		if foot == "" {
			foot = theme.Fit(pn.data.List.Footer, max(0, w-6))
			foot = strings.TrimRight(foot, " ")
		}
		var content []string
		content, pn.lineItems = pluginListLines(rows, more, pn.idx, focused, m.pluginEmpty(pn.data.List.Empty), w-2, h)
		return panel(panelOpts{title: title, bottom: foot, focused: focused}, w, h, content)
	case protocol.WidgetDetail:
		return m.scrollPluginPane(pn, id, title, bottom, m.pluginDetailLines(pn.data.Detail, w-4), focused, w, h, x, y)
	default:
		width := 0
		if def.Wrap {
			width = w - 4
		}
		return m.scrollPluginPane(pn, id, title, bottom, m.pluginTextLines(pn.data.Text, width), focused, w, h, x, y)
	}
}

// maxRowLines is how many lines the selected row of a list may take to show the whole of its first column.
const maxRowLines = 3

// pluginListLines are the lines of a list: a row each, but the selected one takes the lines its first column needs
// (more), so a long title can be read whole. lineItems maps each line drawn to the row it belongs to, for the mouse.
func pluginListLines(rows []listRow, more []listRow, selected int, focused bool, empty string, inner, h int) (content []string, lineItems []int) {
	p := theme.Active()
	if len(rows) == 0 {
		return []string{newLine(p.Panel).add(p.Dim, empty).String()}, nil
	}
	visible := max(1, h-2)
	total := len(rows) + len(more)
	start := max(0, min(selected-visible/2, total-visible))
	if end := selected + len(more); end >= start+visible {
		start = end - visible + 1
	}
	if selected < start {
		start = selected
	}
	line := 0
	draw := func(item int, row listRow) {
		if line >= start && line < start+visible {
			sel := item == selected
			bg := p.Panel
			if sel && focused {
				bg = p.Selection
			}
			l := newLine(bg)
			if sel {
				l.add(p.Accent, "▌")
			} else {
				l.add(p.Panel, " ")
			}
			r := row(bg)
			l.raw(r.String(), r.width())
			content = append(content, l.pad(inner).String())
			lineItems = append(lineItems, item)
		}
		line++
	}
	for i, row := range rows {
		draw(i, row)
		if i == selected {
			for _, r := range more {
				draw(i, r)
			}
		}
	}
	return content, lineItems
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
	// the first column is what a row is about: it keeps two fifths of the width (or all it needs) before the
	// others get any
	minPrimary := max(10, avail*2/5)
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

// pluginListRows are the rows of a list, and more: the lines the selected row needs below it to show the whole of
// its first column.
func (m *Model) pluginListRows(pn *pluginPane, avail int) (rows, more []listRow) {
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
	rows = make([]listRow, 0, len(pn.vis))
	for j, i := range pn.vis {
		it := d.Items[i]
		cell := it.Cells[primary.id]
		color := m.toneColor(cell.Tone)
		first := theme.Fit(cell.Text, primary.w)
		if j == pn.idx && ansi.StringWidth(cell.Text) > primary.w {
			// the start in its column, and the rest below the row, where the other columns leave the whole width
			indent := 0
			if hasIcon {
				indent = 2
			}
			head := wrapSpans([]protocol.Span{{Text: cell.Text}}, primary.w)[0]
			first = theme.Fit(spansText(head), primary.w)
			rest := strings.TrimSpace(strings.TrimPrefix(cell.Text, strings.TrimRight(spansText(head), " ")))
			parts := wrapSpans([]protocol.Span{{Text: rest}}, max(10, avail-indent))
			if len(parts) > maxRowLines-1 {
				parts = parts[:maxRowLines-1]
				last := parts[len(parts)-1]
				last[len(last)-1].Text = strings.TrimRight(ansi.Truncate(last[len(last)-1].Text, max(1, avail-indent-1), ""), " ") + "…"
			}
			for _, part := range parts {
				text := spansText(part)
				more = append(more, func(bg string) *line {
					return newLine(bg).add(p.Text, strings.Repeat(" ", indent)).add(color, text)
				})
			}
		}
		rows = append(rows, func(bg string) *line {
			l := newLine(bg)
			if hasIcon {
				if g, c := m.pluginIcon(it.Icon); g != "" {
					l.add(c, g+" ")
				} else {
					l.add(p.Dim, "  ")
				}
			}
			l.add(color, first)
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
	return rows, more
}

func spansText(spans []protocol.Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
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

// pluginTextLines are the lines of a text, broken to width columns (0: as they are).
func (m *Model) pluginTextLines(t *protocol.TextData, width int) []string {
	p := theme.Active()
	out := make([]string, 0, len(t.Lines))
	for _, spans := range t.Lines {
		if width <= 0 {
			out = append(out, spansLine(m, p.Panel, spans).String())
			continue
		}
		for _, part := range wrapSpans(spans, width) {
			out = append(out, spansLine(m, p.Panel, part).String())
		}
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
			out = append(out, m.titleLines(s.Title, inner)...)
		}
		l := newLine(p.Panel)
		for _, f := range s.Fields {
			color := m.toneColor(f.Tone)
			if f.Tone == "" {
				color = p.Text
			}
			labelW := ansi.StringWidth(f.Label)
			if labelW+ansi.StringWidth(f.Value)+1 > inner {
				// a value that does not fit in a line has lines of its own, under its label
				if l.width() > 0 {
					out = append(out, l.String())
					l = newLine(p.Panel)
				}
				for i, part := range wrapSpans([]protocol.Span{{Text: f.Value}}, max(4, inner-labelW-1)) {
					row := newLine(p.Panel)
					if i == 0 {
						row.add(p.Dim, f.Label+" ")
					} else {
						row.add(p.Dim, strings.Repeat(" ", labelW+1))
					}
					out = append(out, row.add(color, spansText(part)).String())
				}
				continue
			}
			need := labelW + ansi.StringWidth(f.Value) + 4
			if l.width() > 0 && l.width()+need > inner {
				out = append(out, l.String())
				l = newLine(p.Panel)
			}
			field(l, f.Label, f.Value, color)
		}
		if l.width() > 0 {
			out = append(out, l.String())
		}
		text := s.Text
		if textMax > 0 && len(text) > textMax {
			text = text[:textMax]
		}
		for _, spans := range text {
			for _, part := range wrapSpans(spans, inner) {
				out = append(out, spansLine(m, p.Panel, part).String())
			}
		}
		if len(s.Badges) > 0 {
			out = append(out, m.badgeLine(p.Panel, s.Badges, inner).String())
		}
	}
	return out
}

// titleLines is the title of a section: a rule with the title in it, or, when it is too long for that, the title
// broken in lines after the start of the rule.
func (m *Model) titleLines(title string, inner int) []string {
	p := theme.Active()
	if ansi.StringWidth(title)+5 <= inner {
		return []string{m.headerLine(title, inner)}
	}
	var out []string
	for i, part := range wrapSpans([]protocol.Span{{Text: title}}, max(4, inner-3)) {
		lead := "   "
		if i == 0 {
			lead = "── "
		}
		out = append(out, newLine(p.Panel).add(p.Border, lead).add(p.Muted, spansText(part)).String())
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
