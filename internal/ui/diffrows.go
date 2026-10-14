package ui

import (
	"strings"

	"github.com/JGoterris/orbit/internal/git"
)

type rowKind int

const (
	rowFile rowKind = iota
	rowNote
	rowHunk
	rowAdd
	rowDel
	rowCtx
	rowMarker
)

// diffRow is one line of a rendered diff.
type diffRow struct {
	kind rowKind
	text string
	// hunk is the index into diffRows.hunks this row belongs to, -1 for file rows and notes
	hunk int
	// oldNo / newNo are line numbers in the old / new file (0 when there is none)
	oldNo, newNo int
}

// hunkRef says where a hunk starts.
type hunkRef struct {
	file, index int
	// row is the row of its `@@` header
	row int
}

type diffRows struct {
	rows  []diffRow
	hunks []hunkRef
}

func diffTitle(f git.DiffFile) string {
	rename, flag := "", ""
	for _, l := range f.Header {
		switch {
		case strings.HasPrefix(l, "rename from "):
			rename = l[len("rename from "):]
		case strings.HasPrefix(l, "new file mode") && flag == "":
			flag = " (new)"
		case strings.HasPrefix(l, "deleted file mode") && flag == "":
			flag = " (deleted)"
		}
	}
	if rename != "" {
		return rename + " → " + f.Path
	}
	return f.Path + flag
}

// buildRows flattens parsed files into display rows with line numbers, plus where each hunk starts.
func buildRows(files []git.DiffFile) diffRows {
	var out diffRows
	for fi, f := range files {
		out.rows = append(out.rows, diffRow{kind: rowFile, text: diffTitle(f), hunk: -1})
		switch {
		case f.Binary:
			out.rows = append(out.rows, diffRow{kind: rowNote, text: "binary file", hunk: -1})
		case len(f.Hunks) == 0:
			note := "no content changes (mode or empty file)"
			for _, l := range f.Header {
				if strings.HasPrefix(l, "rename ") {
					note = "renamed, no content changes"
				}
			}
			out.rows = append(out.rows, diffRow{kind: rowNote, text: note, hunk: -1})
		}
		for hi, h := range f.Hunks {
			id := len(out.hunks)
			out.hunks = append(out.hunks, hunkRef{file: fi, index: hi, row: len(out.rows)})
			out.rows = append(out.rows, diffRow{kind: rowHunk, text: h.Header, hunk: id})
			o, n := h.OldStart, h.NewStart
			for _, line := range h.Lines {
				c := byte(' ')
				text := ""
				if line != "" {
					c, text = line[0], line[1:]
				}
				switch c {
				case '+':
					out.rows = append(out.rows, diffRow{kind: rowAdd, text: text, hunk: id, newNo: n})
					n++
				case '-':
					out.rows = append(out.rows, diffRow{kind: rowDel, text: text, hunk: id, oldNo: o})
					o++
				case '\\':
					out.rows = append(out.rows, diffRow{kind: rowMarker, text: line, hunk: id})
				default:
					out.rows = append(out.rows, diffRow{kind: rowCtx, text: text, hunk: id, oldNo: o, newNo: n})
					o++
					n++
				}
			}
		}
	}
	return out
}

// hunkAt is the hunk the view is "on": the last one whose header is at or above the first visible row (the first
// one before any).
func hunkAt(hunks []hunkRef, scroll int) int {
	cur := -1
	if len(hunks) > 0 {
		cur = 0
	}
	for i, h := range hunks {
		if h.row <= scroll {
			cur = i
		}
	}
	return cur
}

// currentHunk is the hunk to act on: the explicitly chosen one while any of it is on screen, else the one at the
// top of the view.
func currentHunk(r diffRows, chosen, top, page int) int {
	if len(r.hunks) == 0 {
		return -1
	}
	if chosen >= 0 && chosen < len(r.hunks) {
		end := len(r.rows)
		if chosen+1 < len(r.hunks) {
			end = r.hunks[chosen+1].row
		}
		if r.hunks[chosen].row < top+page && end > top {
			return chosen
		}
	}
	return hunkAt(r.hunks, top)
}

// revealHunk is the scroll position that makes hunk i visible: unchanged if it already is, else its header goes
// to the top.
func revealHunk(r diffRows, i, top, page int) int {
	if i < 0 || i >= len(r.hunks) {
		return top
	}
	end := len(r.rows)
	if i+1 < len(r.hunks) {
		end = r.hunks[i+1].row
	}
	if r.hunks[i].row >= top && end <= top+page {
		return top
	}
	return r.hunks[i].row
}

// splitRow is one row of the side-by-side view: the old side on the left, the new one on the right.
type splitRow struct {
	// full is set for file / hunk / note rows, which span both sides
	full                string
	fullKind            rowKind
	left, right         string
	leftKind, rightKind rowKind
	leftNo, rightNo     int
	hasLeft, hasRight   bool
	hunk                int
}

// buildSplit pairs the removed and added lines of every change side by side.
func buildSplit(r diffRows) []splitRow {
	var out []splitRow
	var dels, adds []diffRow
	flush := func() {
		for i := 0; i < max(len(dels), len(adds)); i++ {
			s := splitRow{}
			if i < len(dels) {
				s.left, s.leftKind, s.leftNo, s.hasLeft, s.hunk = dels[i].text, rowDel, dels[i].oldNo, true, dels[i].hunk
			}
			if i < len(adds) {
				s.right, s.rightKind, s.rightNo, s.hasRight, s.hunk = adds[i].text, rowAdd, adds[i].newNo, true, adds[i].hunk
			}
			out = append(out, s)
		}
		dels, adds = nil, nil
	}
	for _, row := range r.rows {
		switch row.kind {
		case rowDel:
			dels = append(dels, row)
		case rowAdd:
			adds = append(adds, row)
		case rowCtx:
			flush()
			out = append(out, splitRow{left: row.text, right: row.text, leftKind: rowCtx, rightKind: rowCtx,
				leftNo: row.oldNo, rightNo: row.newNo, hasLeft: true, hasRight: true, hunk: row.hunk})
		case rowMarker:
			flush()
			out = append(out, splitRow{full: row.text, fullKind: rowMarker, hunk: row.hunk})
		default:
			flush()
			out = append(out, splitRow{full: row.text, fullKind: row.kind, hunk: row.hunk})
		}
	}
	flush()
	return out
}
