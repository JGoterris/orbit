package plugins

import (
	"strings"
	"unicode"

	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// Limits on what a plugin may hand to the interface, so one that misbehaves cannot make it unusable.
const (
	maxItems      = 5000
	maxLines      = 5000
	maxSections   = 50
	maxFields     = 200
	maxBadges     = 8
	maxTextLength = 4096
)

// Clean makes s safe to draw: no escape sequences nor control characters (a plugin must not be able to move the
// cursor or recolour the terminal), no line breaks, and not absurdly long. Tabs become spaces.
func Clean(s string) string {
	if len(s) > maxTextLength {
		s = s[:maxTextLength]
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString("  ")
		case r == unicode.ReplacementChar:
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func cleanTone(t protocol.Tone) protocol.Tone {
	if protocol.ValidTone(t) {
		return protocol.Tone(Clean(string(t)))
	}
	return protocol.ToneDefault
}

func cleanSpans(spans []protocol.Span) []protocol.Span {
	for i := range spans {
		spans[i].Text = Clean(spans[i].Text)
		spans[i].Tone = cleanTone(spans[i].Tone)
	}
	return spans
}

func cleanBadges(badges []protocol.Badge) []protocol.Badge {
	if len(badges) > maxBadges {
		badges = badges[:maxBadges]
	}
	for i := range badges {
		badges[i].Text = Clean(badges[i].Text)
		badges[i].Icon = Clean(badges[i].Icon)
		badges[i].Tone = cleanTone(badges[i].Tone)
		badges[i].Action = Clean(badges[i].Action)
	}
	return badges
}

func CleanViewData(d *protocol.ViewData) {
	if l := d.List; l != nil {
		if len(l.Items) > maxItems {
			l.Items = l.Items[:maxItems]
		}
		for i := range l.Columns {
			l.Columns[i].ID = Clean(l.Columns[i].ID)
		}
		for i := range l.Items {
			it := &l.Items[i]
			it.Icon = Clean(it.Icon)
			it.Badges = cleanBadges(it.Badges)
			for k, c := range it.Cells {
				c.Text = Clean(c.Text)
				c.Tone = cleanTone(c.Tone)
				it.Cells[k] = c
			}
		}
		l.Empty, l.Footer = Clean(l.Empty), Clean(l.Footer)
	}
	if dt := d.Detail; dt != nil {
		dt.Sections = CleanSections(dt.Sections)
		dt.Empty = Clean(dt.Empty)
	}
	if t := d.Text; t != nil {
		if len(t.Lines) > maxLines {
			t.Lines = t.Lines[len(t.Lines)-maxLines:]
		}
		for i := range t.Lines {
			t.Lines[i] = cleanSpans(t.Lines[i])
		}
		t.Empty = Clean(t.Empty)
	}
}

func CleanSections(sections []protocol.Section) []protocol.Section {
	if len(sections) > maxSections {
		sections = sections[:maxSections]
	}
	for i := range sections {
		s := &sections[i]
		s.Title = Clean(s.Title)
		if len(s.Fields) > maxFields {
			s.Fields = s.Fields[:maxFields]
		}
		for j := range s.Fields {
			f := &s.Fields[j]
			f.Label, f.Value, f.Tone = Clean(f.Label), Clean(f.Value), cleanTone(f.Tone)
		}
		if len(s.Text) > maxLines {
			s.Text = s.Text[:maxLines]
		}
		for j := range s.Text {
			s.Text[j] = cleanSpans(s.Text[j])
		}
		s.Badges = cleanBadges(s.Badges)
	}
	return sections
}

func CleanToast(t *protocol.Toast) {
	if t != nil {
		t.Text, t.Tone = Clean(t.Text), cleanTone(t.Tone)
	}
}
