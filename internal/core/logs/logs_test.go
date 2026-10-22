package logs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func line(seq int, service, text string, stream Stream) Line {
	return Line{Seq: seq, Ts: time.Date(2026, 1, 1, 9, 5, 7, 0, time.Local).UnixMilli(), Service: service, Stream: stream, Text: text}
}

func TestMatcher(t *testing.T) {
	l := line(1, "api", "GET /users 200", Stdout)
	if !Matcher("")(l) || !Matcher("get .*200")(l) || !Matcher("api")(l) {
		t.Error("regex / service matching failed")
	}
	// an invalid regex falls back to a substring
	if !Matcher("(")(line(2, "api", "a ( b", Stdout)) || Matcher("(")(l) {
		t.Error("invalid regex fallback failed")
	}
	if got := FilterLines([]Line{l, line(3, "web", "nope", Stdout)}, "users"); len(got) != 1 {
		t.Errorf("filtered = %d", len(got))
	}
}

func TestFormatLines(t *testing.T) {
	ls := []Line{line(1, "api", "hello", Stdout), line(2, "db", "boot", System)}
	if got := FormatLines(ls, false, false); got != "hello\n» boot" {
		t.Errorf("plain = %q", got)
	}
	if got := FormatLines(ls, true, true); got != "09:05:07 api │ hello\n09:05:07 db  │ » boot" {
		t.Errorf("full = %q", got)
	}
}

func TestReadTail(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.log")
	if got := ReadTail(file, 5, 0); got != nil {
		t.Errorf("missing file = %v", got)
	}
	os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644)
	if got := ReadTail(file, 2, 0); !reflect.DeepEqual(got, []string{"two", "three"}) {
		t.Errorf("last 2 = %v", got)
	}
	if got := ReadTail(file, 0, 0); got != nil {
		t.Errorf("0 lines = %v", got)
	}
	// only the last 8 bytes are read: "three\n" plus a partial "o\n" that must be dropped
	if got := ReadTail(file, 10, 8); !reflect.DeepEqual(got, []string{"three"}) {
		t.Errorf("cut mid-line = %v", got)
	}
}

func TestCleanLine(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mred\x1b[0m":         "red",
		"10%\r50%\r100%":             "100%",
		"a\tb":                       "a  b",
		"bell\x07 and \x1b]0;t\x07x": "bell and x",
		"$ echo a\necho b\n":         "$ echo a echo b",
		"a\r\nb":                     "a b",
	}
	for in, want := range cases {
		if got := CleanLine(in); got != want {
			t.Errorf("CleanLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectLevel(t *testing.T) {
	cases := map[string]Level{"FATAL: x": LevelError, "a warning": LevelWarn, "debug mode": LevelDebug, "hello": LevelNone}
	for in, want := range cases {
		if got := DetectLevel(in); got != want {
			t.Errorf("DetectLevel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoreBoundsAndNotifies(t *testing.T) {
	s := NewStore(3, 5)
	var got []string
	cancel := s.OnLine(func(l Line) { got = append(got, l.Text) })
	for i := 0; i < 10; i++ {
		s.Append("a", Stdout, string(rune('a'+i)))
	}
	if len(got) != 10 {
		t.Errorf("listener saw %d lines", len(got))
	}
	if ls := s.Lines("a"); len(ls) != 3 || ls[0].Text != "h" {
		t.Errorf("per service = %v", ls)
	}
	if ls := s.Lines(""); len(ls) != 5 || ls[4].Text != "j" {
		t.Errorf("all = %v", ls)
	}
	cancel()
	s.Append("a", Stdout, "x")
	if len(got) != 10 {
		t.Error("cancelled listener still called")
	}
	v := s.Version()
	s.Clear("a")
	if len(s.Lines("a")) != 0 || s.Version() == v {
		t.Error("clear did not clear")
	}
}

func TestFileTailFollowsAndDrains(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.log")
	os.WriteFile(file, []byte("old1\nold2\n"), 0o644)
	var got []string
	tail := NewFileTail(file, func(l string) { got = append(got, l) })
	tail.Start(&Backlog{Bytes: 1024, Lines: 1})
	f, _ := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("new1\nnew2")
	f.Close()
	time.Sleep(200 * time.Millisecond)
	tail.Stop(true) // drains the last line without a newline
	if want := []string{"old2", "new1", "new2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %v, want %v", got, want)
	}
}

func TestPipeLines(t *testing.T) {
	var got []string
	PipeLines(strings.NewReader("a\nb\r\nlast"), func(l string) { got = append(got, l) })
	if want := []string{"a", "b\r", "last"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}
