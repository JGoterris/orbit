package ui

import (
	"fmt"
	"strings"

	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// logLayout is how the lines of a log panel are laid out for a given size.
type logLayout struct {
	lines  []logs.Line
	height int
	textW  int
	prefix int
	timeW  int
}

func (m *Model) newLogLayout(lines []logs.Line, w, h int, perService bool) logLayout {
	names := m.names()
	prefixW := 0
	if !perService {
		longest := 4
		for _, n := range names {
			longest = max(longest, len([]rune(n)))
		}
		prefixW = min(14, longest) + 1
	}
	timeW := 0
	if m.showTime {
		timeW = 9
	}
	width := max(10, w-4)
	return logLayout{lines: lines, height: max(1, h-2), prefix: prefixW, timeW: timeW, textW: max(1, width-timeW-prefixW)}
}

func (m *Model) isHead(l logs.Line) bool { return m.fold && l.Trace != nil && l.Trace.Frames >= 2 }

func (m *Model) bodyOf(l logs.Line) string {
	switch {
	case l.Stream == logs.System:
		return "» " + l.Text
	case !m.isHead(l):
		return l.Text
	case !logs.IsFolded(l, m.expanded):
		return "▾ " + l.Text
	}
	t := l.Trace
	summary := ""
	if t.Summary != "" {
		summary = t.Summary + " · "
	}
	return fmt.Sprintf("▸ %s ⋯ %s+%d lines", l.Text, summary, t.Frames)
}

func (lay logLayout) rowsOf(m *Model, l logs.Line) int {
	if !m.wrap {
		return 1
	}
	return max(1, (len([]rune(m.bodyOf(l)))+lay.textW-1)/lay.textW)
}

// window is the slice of lines on screen: first..end, how far back from the bottom it is, and how far it can go.
func (lay logLayout) window(m *Model) (first, end, back, maxBack int) {
	n := len(lay.lines)
	topRows, topLines := 0, 0
	for topLines < n && topRows < lay.height {
		topRows += lay.rowsOf(m, lay.lines[topLines])
		topLines++
	}
	maxBack = max(0, n-topLines)
	back = min(m.scrollBack, maxBack)
	end = n - back
	first = end
	rows := 0
	for first > 0 && rows < lay.height {
		first--
		rows += lay.rowsOf(m, lay.lines[first])
	}
	return first, end, back, maxBack
}

type shownRow struct {
	l    logs.Line
	idx  int
	i    int
	text string
}

// renderLogView is the log panel: title, and the scrolling lines with search / copy-mode highlights.
func (m *Model) renderLogView(title string, w, h int, focused bool) []string {
	p := theme.Active()
	perService := m.logService() != ""
	lines := m.viewLines()
	lay := m.newLogLayout(lines, w, h, perService)
	first, end, back, maxBack := lay.window(m)
	visible := lines[first:end]

	// copy mode: the cursor
	cursorIdx, anchorIdx := -1, -1
	if m.copyCursor != 0 {
		for i, l := range lines {
			if l.Seq == m.copyCursor {
				cursorIdx = i
			}
			if m.copyAnchor != 0 && l.Seq == m.copyAnchor {
				anchorIdx = i
			}
		}
		if m.copyAnchor == 0 {
			anchorIdx = cursorIdx
		}
	}
	selLo, selHi := -1, -1
	if cursorIdx >= 0 {
		selLo, selHi = min(cursorIdx, anchorIdx), max(cursorIdx, anchorIdx)
	}
	var isMatch func(logs.Line) bool
	if m.search != "" {
		isMatch = logs.Matcher(m.search)
	}

	folded := 0
	for _, l := range lines {
		if m.fold && logs.IsFolded(l, m.expanded) {
			folded++
		}
	}
	var parts []string
	if m.copyCursor != 0 {
		if m.copyAnchor != 0 {
			parts = append(parts, fmt.Sprintf("%d selected", selHi-selLo+1))
		} else {
			parts = append(parts, "copy mode")
		}
	}
	if folded > 0 {
		parts = append(parts, fmt.Sprintf("%d traces folded", folded))
	}
	if m.filter != "" {
		parts = append(parts, fmt.Sprintf("/%s  %d matches", m.filter, len(lines)))
	} else {
		parts = append(parts, fmt.Sprintf("%d lines", len(lines)))
	}
	if back > 0 {
		s := fmt.Sprintf("↑ %d", back)
		if m.unread > 0 {
			s += fmt.Sprintf(" · +%d new", m.unread)
		}
		parts = append(parts, s+"  (f to follow)")
	} else {
		parts = append(parts, "following")
	}

	// a line taller than the panel is clipped from its start, like a terminal would
	var shown []shownRow
	for k, l := range visible {
		body := m.bodyOf(l)
		idx := first + k
		r := []rune(body)
		if !m.wrap || len(r) <= lay.textW {
			shown = append(shown, shownRow{l, idx, 0, theme.Fit(body, lay.textW)})
			continue
		}
		for at, i := 0, 0; at < len(r); at, i = at+lay.textW, i+1 {
			chunk := string(r[at:min(len(r), at+lay.textW)])
			shown = append(shown, shownRow{l, idx, i, chunk + strings.Repeat(" ", max(0, lay.textW-len([]rune(chunk))))})
		}
	}
	if len(shown) > lay.height {
		shown = shown[len(shown)-lay.height:]
	}

	names := m.names()
	var content []string
	rowSeqs := make([]int, 0, len(shown))
	if len(visible) == 0 {
		msg := "no output yet"
		if m.filter != "" {
			msg = "no lines match the filter"
		}
		content = append(content, newLine(p.Panel).add(p.Dim, msg).String())
	}
	for _, r := range shown {
		l := r.l
		level := logs.LevelNone
		if l.Stream != logs.System {
			level = logs.DetectLevel(l.Text)
		}
		color := p.Text
		switch {
		case l.Stream == logs.System:
			color = p.Accent2
		case m.fold && l.TraceOf != 0:
			color = p.Muted
		case l.Trace != nil:
			color = p.Red
		case level == logs.LevelError:
			color = p.Red
		case level == logs.LevelWarn:
			color = p.Yellow
		case level == logs.LevelDebug:
			color = p.Muted
		case l.Stream == logs.Stderr:
			color = p.Orange
		}
		bg := ""
		switch {
		case r.idx >= selLo && r.idx <= selHi && selLo >= 0:
			bg = p.Selection
			if r.idx == cursorIdx {
				bg = p.Cursor
			}
		case m.match != 0 && l.Seq == m.match:
			bg = p.Cursor
		case isMatch != nil && isMatch(l):
			bg = p.Match
		}
		ln := newLine(p.Panel)
		if r.i > 0 {
			ln.addStyled(p.Text, bg, false, strings.Repeat(" ", lay.timeW+lay.prefix))
		} else {
			if m.showTime {
				ln.addStyled(p.Dim, bg, false, logs.Clock(l.Ts)+" ")
			}
			if !perService {
				ln.addStyled(theme.ServiceColor(l.Service, names), bg, false, theme.Fit(l.Service, lay.prefix-1)+" ")
			}
		}
		ln.addStyled(color, bg, false, r.text)
		content = append(content, ln.String())
		rowSeqs = append(rowSeqs, l.Seq)
	}

	m.geo.mu.Lock()
	m.geo.logW, m.geo.logH = w, h
	m.geo.logRows = rowSeqs
	m.geo.maxBack = maxBack
	m.geo.first, m.geo.end = first, end
	m.geo.mu.Unlock()

	return panel(panelOpts{title: title, bottom: strings.Join(parts, " · "), focused: focused, padX: 1}, w, h, content)
}
