// Package ui is orbit's terminal interface: a Bubble Tea model that draws the dashboard, the dependency graph and
// the logs over any supervisor (local or remote).
package ui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/ui/theme"
)

type styleKey struct {
	fg, bg string
	bold   bool
}

var styles sync.Map

func styleOf(fg, bg string, bold bool) lipgloss.Style {
	k := styleKey{fg, bg, bold}
	if s, ok := styles.Load(k); ok {
		return s.(lipgloss.Style)
	}
	s := lipgloss.NewStyle()
	if fg != "" {
		s = s.Foreground(lipgloss.Color(fg))
	}
	if bg != "" {
		s = s.Background(lipgloss.Color(bg))
	}
	if bold {
		s = s.Bold(true)
	}
	styles.Store(k, s)
	return s
}

// paint wraps text in the colours (either may be ""); text must not contain newlines.
func paint(fg, bg string, bold bool, text string) string {
	if text == "" || (fg == "" && bg == "" && !bold) {
		return text
	}
	return styleOf(fg, bg, bold).Render(text)
}

// line builds one row of the screen out of coloured segments, tracking its width.
type line struct {
	bg string
	sb strings.Builder
	w  int
}

func newLine(bg string) *line { return &line{bg: bg} }

func (l *line) add(fg, text string) *line  { return l.addStyled(fg, "", false, text) }
func (l *line) bold(fg, text string) *line { return l.addStyled(fg, "", true, text) }

func (l *line) addStyled(fg, bg string, bold bool, text string) *line {
	if text == "" {
		return l
	}
	if bg == "" {
		bg = l.bg
	}
	l.sb.WriteString(paint(fg, bg, bold, text))
	l.w += ansi.StringWidth(text)
	return l
}

// raw appends an already styled string of the given visible width.
func (l *line) raw(s string, width int) *line {
	l.sb.WriteString(s)
	l.w += width
	return l
}

func (l *line) width() int { return l.w }

// pad fills the line with the background up to width columns.
func (l *line) pad(width int) *line {
	if l.w < width {
		l.sb.WriteString(paint("", l.bg, false, strings.Repeat(" ", width-l.w)))
		l.w = width
	}
	return l
}

func (l *line) String() string { return l.sb.String() }

// clamp cuts or pads a styled line to exactly width columns (padding with bg).
func clamp(s string, width int, bg string) string {
	w := ansi.StringWidth(s)
	switch {
	case w > width:
		return ansi.Truncate(s, width, "")
	case w < width:
		return s + paint("", bg, false, strings.Repeat(" ", width-w))
	}
	return s
}

// blank is a row of spaces on the background.
func blank(width int, bg string) string { return paint("", bg, false, strings.Repeat(" ", width)) }

// panelOpts describes a bordered box.
type panelOpts struct {
	title   string
	bottom  string // right aligned on the bottom border
	focused bool
	padX    int
	bg      string
}

// panel draws a rounded box of w x h around content (one styled string per row).
func panel(o panelOpts, w, h int, content []string) []string {
	if w < 4 || h < 2 {
		return fill(w, h, o.bg)
	}
	p := theme.Active()
	border := p.Border
	if o.focused {
		border = p.BorderFocus
	}
	bg := o.bg
	if bg == "" {
		bg = p.Panel
	}
	b := func(s string) string { return paint(border, bg, false, s) }
	title := func(t string) string {
		if t == "" {
			return ""
		}
		return paint(p.Text, bg, false, " "+t+" ")
	}

	top := b("╭─") + title(o.title)
	topW := 2 + ansi.StringWidth(title(o.title))
	if tw := w - 1; topW > tw {
		top = ansi.Truncate(top, tw, "")
		topW = tw
	}
	top += b(strings.Repeat("─", max(0, w-1-topW)) + "╮")

	botTitle := title(o.bottom)
	botW := ansi.StringWidth(botTitle)
	var bot string
	if botW > 0 && botW+3 <= w {
		bot = b("╰"+strings.Repeat("─", w-2-botW-1)) + botTitle + b("─╯")
	} else {
		bot = b("╰" + strings.Repeat("─", w-2) + "╯")
	}

	out := make([]string, 0, h)
	out = append(out, top)
	pad := strings.Repeat(" ", o.padX)
	inner := w - 2 - 2*o.padX
	for i := 0; i < h-2; i++ {
		row := ""
		if i < len(content) {
			row = content[i]
		}
		row = clamp(row, inner, bg)
		out = append(out, b("│")+paint("", bg, false, pad)+row+paint("", bg, false, pad)+b("│"))
	}
	return append(out, bot)
}

// fill is a w x h block of background.
func fill(w, h int, bg string) []string {
	out := make([]string, max(0, h))
	for i := range out {
		out[i] = blank(w, bg)
	}
	return out
}

// hconcat puts blocks side by side (all the same height).
func hconcat(blocks ...[]string) []string {
	if len(blocks) == 0 {
		return nil
	}
	h := len(blocks[0])
	out := make([]string, h)
	for i := 0; i < h; i++ {
		var sb strings.Builder
		for _, b := range blocks {
			if i < len(b) {
				sb.WriteString(b[i])
			}
		}
		out[i] = sb.String()
	}
	return out
}

// overlay draws modal (rows of equal width mw) over base at column x, row y.
func overlay(base []string, modal []string, mw, x, y, screenW int) []string {
	out := append([]string(nil), base...)
	for i, m := range modal {
		r := y + i
		if r < 0 || r >= len(out) {
			continue
		}
		left := ansi.Cut(out[r], 0, x)
		right := ansi.Cut(out[r], x+mw, screenW)
		out[r] = left + m + right
	}
	return out
}

// modal is a centred box with an accent border over the whole screen.
func modalBox(title string, w, h int, content []string) []string {
	p := theme.Active()
	return panel(panelOpts{title: title, focused: true, padX: 1, bg: p.PanelAlt}, w, h, content)
}

// center places a w x h block in the middle of a screen.
func center(screenW, screenH, w, h int) (x, y int) {
	return max(0, (screenW-w)/2), max(0, (screenH-h)/2)
}
