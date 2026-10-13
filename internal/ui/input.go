package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// input is a one-line text field.
type input struct {
	text []rune
	pos  int
}

func (in *input) value() string { return string(in.text) }

func (in *input) set(s string) {
	in.text = []rune(s)
	in.pos = len(in.text)
}

// handle applies an editing key and reports whether it was one.
func (in *input) handle(k tea.KeyMsg) bool {
	switch k.Type {
	case tea.KeyRunes:
		if k.Alt {
			return false
		}
		in.text = append(in.text[:in.pos], append(append([]rune(nil), k.Runes...), in.text[in.pos:]...)...)
		in.pos += len(k.Runes)
	case tea.KeySpace:
		in.text = append(in.text[:in.pos], append([]rune{' '}, in.text[in.pos:]...)...)
		in.pos++
	case tea.KeyBackspace:
		if in.pos > 0 {
			in.text = append(in.text[:in.pos-1], in.text[in.pos:]...)
			in.pos--
		}
	case tea.KeyDelete:
		if in.pos < len(in.text) {
			in.text = append(in.text[:in.pos], in.text[in.pos+1:]...)
		}
	case tea.KeyLeft:
		in.pos = max(0, in.pos-1)
	case tea.KeyRight:
		in.pos = min(len(in.text), in.pos+1)
	case tea.KeyHome, tea.KeyCtrlA:
		in.pos = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		in.pos = len(in.text)
	case tea.KeyCtrlU:
		in.text = in.text[in.pos:]
		in.pos = 0
	case tea.KeyCtrlK:
		in.text = in.text[:in.pos]
	case tea.KeyCtrlW:
		i := in.pos
		for i > 0 && in.text[i-1] == ' ' {
			i--
		}
		for i > 0 && in.text[i-1] != ' ' {
			i--
		}
		in.text = append(in.text[:i], in.text[in.pos:]...)
		in.pos = i
	default:
		return false
	}
	return true
}

// view draws the field in `width` columns, with the cursor, or the placeholder when empty.
func (in *input) view(width int, fg, dim, bg string, focused bool) string {
	l := newLine(bg)
	if len(in.text) == 0 {
		return l.add(dim, "").String()
	}
	// scroll so the cursor stays visible
	start := 0
	if in.pos >= width {
		start = in.pos - width + 1
	}
	visible := in.text[start:min(len(in.text), start+width)]
	for i, r := range visible {
		if focused && start+i == in.pos {
			l.addStyled(bg, fg, false, string(r))
		} else {
			l.add(fg, string(r))
		}
	}
	if focused && in.pos == len(in.text) && len(visible) < width {
		l.addStyled(bg, fg, false, " ")
	}
	return l.String()
}

// viewWithPlaceholder is view, but shows placeholder (dim) when the field is empty.
func (in *input) viewWithPlaceholder(width int, placeholder, fg, dim, bg string, focused bool) string {
	if len(in.text) > 0 {
		return in.view(width, fg, dim, bg, focused)
	}
	l := newLine(bg)
	ph := []rune(placeholder)
	if len(ph) > width-1 {
		ph = ph[:max(0, width-1)]
	}
	if focused {
		l.addStyled(bg, fg, false, " ")
	}
	l.add(dim, string(ph))
	return l.String()
}

func trimTo(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " ")
}
