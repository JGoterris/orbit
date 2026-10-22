package cli

import (
	"fmt"
	"os"

	xansi "github.com/charmbracelet/x/ansi"
)

var useColor = func() bool {
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == ""
}()

func ansi(code string) func(string) string {
	return func(s string) string {
		if !useColor {
			return s
		}
		return fmt.Sprintf("\x1b[%sm%s\x1b[0m", code, s)
	}
}

// Colours for terminal output; they are no-ops when stdout is not a terminal or NO_COLOR is set.
var (
	dim    = ansi("2")
	bold   = ansi("1")
	red    = ansi("31")
	green  = ansi("32")
	yellow = ansi("33")
	cyan   = ansi("36")
)

// errorln prints msg to stderr with the orbit: prefix.
func errorln(msg string) { fmt.Fprintln(os.Stderr, red("orbit:")+" "+msg) }

// cellWidth is the width of s in terminal columns.
func cellWidth(s string) int { return xansi.StringWidth(s) }

// padEnd pads s with spaces to width columns.
func padEnd(s string, width int) string {
	n := cellWidth(s)
	for ; n < width; n++ {
		s += " "
	}
	return s
}
