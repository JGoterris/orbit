//go:build !windows

package term

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/core/console"
)

// run starts argv on a PTY and feeds a Terminal with its output, the way the console overlay will.
func run(t *testing.T, cols, rows int, argv ...string) (*console.Session, *Terminal) {
	t.Helper()
	s, err := console.NewSession(console.Spec{Argv: argv, Env: map[string]string{}}, cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	term := New(cols, rows, writerFunc(func(b []byte) { s.Write(b) }))
	s.Attach(func(b []byte) { term.Write(b) })
	t.Cleanup(func() { s.Kill(); term.Close() })
	return s, term
}

type writerFunc func([]byte)

func (f writerFunc) Write(b []byte) (int, error) { f(b); return len(b), nil }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPlainOutputAndColours(t *testing.T) {
	s, term := run(t, 40, 5, "/bin/sh", "-c", `printf '\033[31mred\033[0m plain\nsecond line\n'`)
	<-s.Done()
	eventually(t, "the text", func() bool { return strings.HasPrefix(term.Lines()[0], "red plain") })
	if got := term.Lines()[1]; got != "second line" {
		t.Fatalf("line 1 = %q", got)
	}
	c, _ := term.CellAt(0, 0)
	if c.Style.Fg == nil {
		t.Fatalf("the red cell has no foreground colour: %+v", c)
	}
	if plain, _ := term.CellAt(5, 0); plain.Style.Fg != nil {
		t.Fatalf("the plain text must have the default colour: %+v", plain)
	}
	if !strings.Contains(term.Render(), "\x1b[") {
		t.Fatal("Render must keep the styles")
	}
}

func TestCursorMovesAndAlternateScreen(t *testing.T) {
	s, term := run(t, 30, 6, "/bin/sh", "-c", `printf '\033[?1049h\033[3;5Hhello'; sleep 30`)
	eventually(t, "the alternate screen", func() bool { return term.IsAltScreen() && strings.Contains(term.Lines()[2], "hello") })
	if line := term.Lines()[2]; line != "    hello" {
		t.Fatalf("line 2 = %q", line)
	}
	x, y, _ := term.Cursor()
	if x != 9 || y != 2 {
		t.Fatalf("cursor = %d,%d", x, y)
	}
	s.Kill()
}

func TestResizeIsSeenByTheProgram(t *testing.T) {
	s, term := run(t, 40, 10, "/bin/sh", "-c", `trap 'stty size' WINCH; stty size; while :; do sleep 0.1; done`)
	eventually(t, "the first size", func() bool { return strings.Contains(term.String(), "10 40") })
	s.Resize(60, 20)
	term.Resize(60, 20)
	eventually(t, "the new size", func() bool { return strings.Contains(term.String(), "20 60") })
}

func TestAnswersTheProgramsQueries(t *testing.T) {
	// a cursor position report goes through the emulator back to the program's stdin
	s, term := run(t, 40, 5, "/bin/sh", "-c", `printf '\033[6n'; IFS=R read -r -d R reply; printf 'reply=%s' "$(printf %s "$reply" | od -c | head -1)"`)
	eventually(t, "the reply", func() bool { return strings.Contains(term.String(), "reply=") })
	_ = s
}

func TestFullScreenEditor(t *testing.T) {
	nvim, err := exec.LookPath("nvim")
	if err != nil {
		t.Skip("nvim is not installed")
	}
	if os.Getenv("ORBIT_SKIP_NVIM") != "" {
		t.Skip()
	}
	s, term := run(t, 80, 24, nvim, "--clean", "-u", "NONE", "-n")
	eventually(t, "nvim on the alternate screen", func() bool { return term.IsAltScreen() && strings.Contains(term.String(), "~") })
	s.Write([]byte("ihello from nvim\x1b"))
	eventually(t, "the typed text", func() bool { return strings.Contains(term.Lines()[0], "hello from nvim") })

	s.Resize(100, 30)
	term.Resize(100, 30)
	eventually(t, "the redraw after a resize", func() bool {
		lines := term.Lines()
		return len(lines) == 30 && strings.Contains(lines[0], "hello from nvim")
	})
	s.Write([]byte(":q!\r"))
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("nvim did not quit")
	}
	if s.ExitCode() != 0 {
		t.Fatalf("nvim exit code %d", s.ExitCode())
	}
}
