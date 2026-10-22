package graphlayout

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Cell is one character of the painted grid.
type Cell struct {
	Ch   string
	Fg   string
	Bg   string
	Bold bool
}

// NodeStyle says how one service's box looks.
type NodeStyle struct {
	Icon          string
	IconColor     string
	Title         string
	Subtitle      string
	SubtitleColor string
	Border        string
	Selected      bool
}

type PaintOptions struct {
	Node func(name string) NodeStyle
	// EdgeColor returns "" for an edge with no special colour
	EdgeColor   func(edge string) string
	DefaultEdge string
	Text        string
}

var lineChars = map[uint8]string{
	dirE: "─", dirW: "─", dirE | dirW: "─",
	dirN: "│", dirS: "│", dirN | dirS: "│",
	dirE | dirS: "╭", dirW | dirS: "╮", dirN | dirE: "╰", dirN | dirW: "╯",
	dirN | dirS | dirE: "├", dirN | dirS | dirW: "┤",
	dirE | dirW | dirS: "┬", dirE | dirW | dirN: "┴",
	dirN | dirE | dirS | dirW: "┼",
}

func truncate(s string, w int) string { return ansi.Truncate(s, max(0, w), "…") }

// Paint draws the layout onto a grid of cells.
func Paint(l *Layout, opts PaintOptions) [][]Cell {
	width, height := l.Width, l.Height
	grid := make([][]Cell, height)
	for y := range grid {
		grid[y] = make([]Cell, width)
		for x := range grid[y] {
			grid[y][x] = Cell{Ch: " ", Fg: opts.Text}
		}
	}
	pickColor := func(edges edgeSet) string {
		for _, e := range edges {
			if opts.EdgeColor != nil {
				if c := opts.EdgeColor(e); c != "" {
					return c
				}
			}
		}
		return opts.DefaultEdge
	}

	for i, m := range l.masks {
		if m == 0 {
			continue
		}
		ch, ok := lineChars[m]
		if !ok {
			ch = "·"
		}
		grid[i/width][i%width] = Cell{Ch: ch, Fg: pickColor(l.cellEdges[i])}
	}
	for i, edges := range l.arrows {
		grid[i/width][i%width] = Cell{Ch: "▶", Fg: pickColor(edges), Bold: true}
	}

	for _, b := range l.Boxes {
		st := opts.Node(b.Name)
		tl, tr, bl, br, h, v := "╭", "╮", "╰", "╯", "─", "│"
		if st.Selected {
			tl, tr, bl, br, h, v = "╔", "╗", "╚", "╝", "═", "║"
		}
		put := func(cx, cy int, c Cell) {
			if cy >= 0 && cy < height && cx >= 0 && cx < width {
				grid[cy][cx] = c
			}
		}
		border := func(ch string) Cell { return Cell{Ch: ch, Fg: st.Border, Bold: st.Selected} }
		put(b.X, b.Y, border(tl))
		put(b.X+b.W-1, b.Y, border(tr))
		put(b.X, b.Y+b.H-1, border(bl))
		put(b.X+b.W-1, b.Y+b.H-1, border(br))
		for cx := b.X + 1; cx < b.X+b.W-1; cx++ {
			put(cx, b.Y, border(h))
			put(cx, b.Y+b.H-1, border(h))
		}
		for cy := b.Y + 1; cy < b.Y+b.H-1; cy++ {
			put(b.X, cy, border(v))
			put(b.X+b.W-1, cy, border(v))
			for cx := b.X + 1; cx < b.X+b.W-1; cx++ {
				put(cx, cy, Cell{Ch: " ", Fg: opts.Text})
			}
		}
		inner := b.W - 4
		put(b.X+2, b.Y+1, Cell{Ch: st.Icon, Fg: st.IconColor, Bold: true})
		// wide characters take two cells; the second stays empty
		text := func(x, y int, s string, c Cell) {
			for _, ch := range s {
				c.Ch = string(ch)
				put(x, y, c)
				if ansi.StringWidth(c.Ch) == 2 {
					x++
					c.Ch = ""
					put(x, y, c)
				}
				x++
			}
		}
		text(b.X+4, b.Y+1, truncate(st.Title, inner-2), Cell{Fg: opts.Text, Bold: true})
		text(b.X+2, b.Y+2, truncate(st.Subtitle, inner), Cell{Fg: st.SubtitleColor})
	}
	for i, edges := range l.ports {
		cy, cx := i/width, i%width
		cell := grid[cy][cx]
		if cell.Ch != "│" && cell.Ch != "║" {
			continue
		}
		highlight := ""
		if opts.EdgeColor != nil {
			for _, e := range edges {
				if c := opts.EdgeColor(e); c != "" {
					highlight = c
					break
				}
			}
		}
		if cell.Ch == "│" {
			cell.Ch = "├"
		} else {
			cell.Ch = "╟"
		}
		if highlight != "" {
			cell.Fg = highlight
		}
		grid[cy][cx] = cell
	}
	return grid
}

// GridString flattens a grid to plain text, trimming the end of every row.
func GridString(grid [][]Cell) string {
	rows := make([]string, len(grid))
	for y, r := range grid {
		var b strings.Builder
		for _, c := range r {
			b.WriteString(c.Ch)
		}
		rows[y] = strings.TrimRight(b.String(), " ")
	}
	return strings.Join(rows, "\n")
}
