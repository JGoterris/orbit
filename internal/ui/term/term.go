// Package term is a terminal emulator widget: it takes the bytes a program writes to its PTY and keeps the screen
// they paint, ready to be drawn inside the TUI.
package term

import (
	"io"
	"strings"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Terminal is the screen of one program.
type Terminal struct {
	// mu guards the screen: cells are read through pointers into the emulator's buffer, so the lock has to cover
	// the whole read, which the emulator's own SafeEmulator cannot do.
	mu     sync.RWMutex
	emu    *vt.Emulator
	closed bool
}

// Cell is a copy of one cell of the screen.
type Cell = uv.Cell

// New makes a terminal of cols x rows. Whatever the emulator answers to the program's queries (cursor position,
// device attributes...) is written to reply, which must be the program's PTY; without it a program that asks
// would wait forever, and the emulator's pipe would block.
func New(cols, rows int, reply io.Writer) *Terminal {
	t := &Terminal{emu: vt.NewEmulator(cols, rows)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := t.emu.Read(buf)
			if n > 0 && reply != nil {
				reply.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return t
}

// Write feeds the program's output to the emulator.
func (t *Terminal) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, io.ErrClosedPipe
	}
	return t.emu.Write(p)
}

func (t *Terminal) Resize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.emu.Resize(cols, rows)
}

func (t *Terminal) Width() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.emu.Width()
}

func (t *Terminal) Height() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.emu.Height()
}

// IsAltScreen: full-screen programs (vim, less, top) draw on the alternate screen.
func (t *Terminal) IsAltScreen() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.emu.IsAltScreen()
}

// Cursor is the position of the cursor and whether the program hid it.
func (t *Terminal) Cursor() (x, y int, hidden bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p := t.emu.CursorPosition()
	return p.X, p.Y, t.emu.CursorHidden()
}

// CellAt is a copy of the cell at x, y; ok is false outside the screen.
func (t *Terminal) CellAt(x, y int) (c Cell, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p := t.emu.CellAt(x, y)
	if p == nil {
		return Cell{}, false
	}
	return *p, true
}

// Lines is the screen as plain text, one string per row, without trailing spaces.
func (t *Terminal) Lines() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	h, w := t.emu.Height(), t.emu.Width()
	lines := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			c := t.emu.CellAt(x, y)
			switch {
			case c == nil:
				b.WriteByte(' ')
			case c.Content != "":
				b.WriteString(c.Content)
			case c.Width > 0:
				b.WriteByte(' ')
			}
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	return lines
}

// String is Lines joined with newlines.
func (t *Terminal) String() string { return strings.Join(t.Lines(), "\n") }

// Render is the screen as a string with its styles encoded as ANSI escape codes.
func (t *Terminal) Render() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.emu.Render()
}

// Close stops forwarding replies and makes later writes fail. It closes the emulator's reply pipe instead of the
// emulator itself: Emulator.Close flips a flag that its reader reads without synchronisation.
func (t *Terminal) Close() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	if c, ok := t.emu.InputPipe().(io.Closer); ok {
		c.Close()
	}
}

// StyledLines is the screen as rows of exactly Width columns with their colours and attributes encoded as ANSI
// escape codes. When showCursor is true and the program has not hidden it, the cursor cell is drawn reversed.
func (t *Terminal) StyledLines(showCursor bool) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	h, w := t.emu.Height(), t.emu.Width()
	cur := t.emu.CursorPosition()
	curVisible := showCursor && !t.emu.CursorHidden()
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			c := t.emu.CellAt(x, y)
			content, style := " ", uv.Style{}
			if c != nil {
				style = c.Style
				if c.Content != "" {
					content = c.Content
				} else if c.Width == 0 {
					continue // the second half of a wide character
				}
			}
			if curVisible && x == cur.X && y == cur.Y {
				style.Attrs ^= uv.AttrReverse
			}
			b.WriteString(style.Styled(content))
		}
		rows[y] = b.String()
	}
	return rows
}
