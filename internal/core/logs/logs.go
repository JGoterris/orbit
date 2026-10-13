// Package logs holds what services print: the line type, the in-memory store, helpers to clean, filter and fold
// lines, and the tailers that follow log files and pipes.
package logs

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
	System Stream = "system"
)

// Line is one line of a service's output (or one of orbit's own, on the system stream).
type Line struct {
	Seq     int    `json:"seq"`
	Ts      int64  `json:"ts"`
	Service string `json:"service"`
	Stream  Stream `json:"stream"`
	Text    string `json:"text"`
	// Trace is set on the first line of a stack trace
	Trace *TraceInfo `json:"trace,omitempty"`
	// TraceOf is, on the other lines of a stack trace, the seq of its first line (0 when not part of one)
	TraceOf int `json:"traceOf,omitempty"`
}

// Ring is a fixed-size FIFO buffer.
type Ring[T any] struct {
	items    []T
	capacity int
}

func NewRing[T any](capacity int) *Ring[T] { return &Ring[T]{capacity: capacity} }

func (r *Ring[T]) Push(item T) {
	r.items = append(r.items, item)
	// trim in chunks so we don't copy on every push
	if len(r.items) > r.capacity+r.capacity/4 {
		r.items = append([]T(nil), r.items[len(r.items)-r.capacity:]...)
	}
}

// Items returns a copy of what the ring holds, oldest first.
func (r *Ring[T]) Items() []T {
	items := r.items
	if len(items) > r.capacity {
		items = items[len(items)-r.capacity:]
	}
	return append([]T(nil), items...)
}

func (r *Ring[T]) Len() int { return min(len(r.items), r.capacity) }
func (r *Ring[T]) Clear()   { r.items = nil }

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
var controlRe = regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f]`)

func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// CleanLine cleans a raw line: ANSI sequences, carriage-return progress bars, tabs and control characters.
func CleanLine(raw string) string {
	s := StripANSI(raw)
	if i := strings.LastIndex(s, "\r"); i != -1 {
		s = s[i+1:]
	}
	s = strings.ReplaceAll(s, "\t", "  ")
	return controlRe.ReplaceAllString(s, "")
}

type Level string

const (
	LevelNone  Level = ""
	LevelError Level = "error"
	LevelWarn  Level = "warn"
	LevelInfo  Level = "info"
	LevelDebug Level = "debug"
)

var (
	errorRe = regexp.MustCompile(`(?i)\b(error|err|fatal|panic|exception|failed|traceback)\b`)
	warnRe  = regexp.MustCompile(`(?i)\b(warn|warning|deprecated)\b`)
	debugRe = regexp.MustCompile(`(?i)\b(debug|trace|verbose)\b`)
)

func DetectLevel(text string) Level {
	switch {
	case errorRe.MatchString(text):
		return LevelError
	case warnRe.MatchString(text):
		return LevelWarn
	case debugRe.MatchString(text):
		return LevelDebug
	}
	return LevelNone
}

// Matcher builds a line predicate from a user filter: a case-insensitive regular expression, or a plain substring
// when it is not a valid one. It matches the text and the service name.
func Matcher(filter string) func(Line) bool {
	if filter == "" {
		return func(Line) bool { return true }
	}
	if re, err := regexp.Compile("(?i)" + filter); err == nil {
		return func(l Line) bool { return re.MatchString(l.Text) || re.MatchString(l.Service) }
	}
	f := strings.ToLower(filter)
	return func(l Line) bool {
		return strings.Contains(strings.ToLower(l.Text), f) || strings.Contains(l.Service, f)
	}
}

func FilterLines(lines []Line, filter string) []Line {
	if filter == "" {
		return lines
	}
	keep := Matcher(filter)
	var out []Line
	for _, l := range lines {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out
}

// IsFolded: a trace head is folded when it has more than one line behind it and was not expanded.
func IsFolded(l Line, expanded map[int]bool) bool {
	return l.Trace != nil && l.Trace.Frames >= 2 && !expanded[l.Seq]
}

// FoldTraces hides the lines of folded stack traces. A trace whose head is not in lines (rotated out, filtered)
// stays visible.
func FoldTraces(lines []Line, expanded map[int]bool) []Line {
	folded := map[int]bool{}
	out := make([]Line, 0, len(lines))
	for _, l := range lines {
		if IsFolded(l, expanded) {
			folded[l.Seq] = true
		}
		if l.TraceOf != 0 && folded[l.TraceOf] {
			continue
		}
		out = append(out, l)
	}
	return out
}

// WithFrames is, for copying, the lines of rng plus the hidden lines of every stack trace whose head is in it.
func WithFrames(rng, all []Line) []Line {
	seqs := map[int]bool{}
	heads := map[int]bool{}
	for _, l := range rng {
		seqs[l.Seq] = true
		if l.Trace != nil {
			heads[l.Seq] = true
		}
	}
	var out []Line
	for _, l := range all {
		if seqs[l.Seq] || (l.TraceOf != 0 && heads[l.TraceOf]) {
			out = append(out, l)
		}
	}
	return out
}

// Clock formats a timestamp as HH:MM:SS in local time.
func Clock(ts int64) string { return time.UnixMilli(ts).Format("15:04:05") }

// FormatLines is plain text for copying/exporting: one line per entry, optionally with time and service prefix.
func FormatLines(lines []Line, time, prefix bool) string {
	width := 0
	if prefix {
		for _, l := range lines {
			width = max(width, len([]rune(l.Service)))
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		var head []string
		if time {
			head = append(head, Clock(l.Ts))
		}
		if prefix {
			head = append(head, fmt.Sprintf("%-*s │", width, l.Service))
		}
		text := l.Text
		if l.Stream == System {
			text = "» " + text
		}
		if len(head) > 0 {
			text = strings.Join(head, " ") + " " + text
		}
		out[i] = text
	}
	return strings.Join(out, "\n")
}

// ReadTail returns the last `lines` lines of a file, reading at most the last `bytes` bytes. Empty if the file
// does not exist.
func ReadTail(path string, lines int, bytes int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 || lines <= 0 {
		return nil
	}
	if bytes <= 0 {
		bytes = 256 * 1024
	}
	size := st.Size()
	start := max(0, size-bytes)
	buf := make([]byte, size-start)
	n, _ := f.ReadAt(buf, start)
	text := string(buf[:n])
	// a cut in the middle of a line is not worth showing
	if start > 0 {
		text = text[strings.Index(text, "\n")+1:]
	}
	out := strings.Split(text, "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) > lines {
		out = out[len(out)-lines:]
	}
	return out
}

// Store keeps the lines of every service in memory, bounded, and tells listeners about each new one.
type Store struct {
	mu          sync.Mutex
	seq         int
	all         *Ring[*Line]
	perService  map[string]*Ring[*Line]
	perCapacity int
	listeners   map[int]func(Line)
	nextL       int
	traces      *TraceDetector
}

func NewStore(perServiceCapacity, allCapacity int) *Store {
	if perServiceCapacity <= 0 {
		perServiceCapacity = 5000
	}
	if allCapacity <= 0 {
		allCapacity = 20000
	}
	return &Store{
		all: NewRing[*Line](allCapacity), perService: map[string]*Ring[*Line]{}, perCapacity: perServiceCapacity,
		listeners: map[int]func(Line){}, traces: NewTraceDetector(),
	}
}

// Append adds a line a service printed (cleaned of ANSI and control characters).
func (s *Store) Append(service string, stream Stream, text string) {
	s.add(Line{Ts: time.Now().UnixMilli(), Service: service, Stream: stream, Text: CleanLine(text)})
}

// Ingest adds a line produced elsewhere (a remote supervisor), keeping its time and text; the sequence number is
// ours and its trace tags are detected again.
func (s *Store) Ingest(l Line) {
	l.Trace, l.TraceOf = nil, 0
	s.add(l)
}

func (s *Store) add(l Line) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	l.Seq = s.seq
	// lines are shared by pointer: tagging the head of a stack trace once its first frame arrives updates the
	// copy held by every ring
	p := &l
	s.traces.Feed(p)
	s.all.Push(p)
	ring := s.perService[l.Service]
	if ring == nil {
		ring = NewRing[*Line](s.perCapacity)
		s.perService[l.Service] = ring
	}
	ring.Push(p)
	// listeners run under the lock so lines reach every one of them in order: they must not call back into the store
	for _, f := range s.listeners {
		f(*p)
	}
}

func values(items []*Line) []Line {
	out := make([]Line, len(items))
	for i, p := range items {
		out[i] = *p
	}
	return out
}

// Lines are the lines of one service, or of all of them when service is "", oldest first.
func (s *Store) Lines(service string) []Line {
	s.mu.Lock()
	defer s.mu.Unlock()
	if service == "" {
		return values(s.all.Items())
	}
	if r := s.perService[service]; r != nil {
		return values(r.Items())
	}
	return nil
}

func (s *Store) Clear(service string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if service == "" {
		s.all.Clear()
		s.perService = map[string]*Ring[*Line]{}
		s.traces.Reset("")
	} else {
		s.traces.Reset(service)
		if r := s.perService[service]; r != nil {
			r.Clear()
		}
	}
	s.seq++
}

// Version changes whenever the content does.
func (s *Store) Version() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// OnLine calls fn for every new line; the returned function stops it.
func (s *Store) OnLine(fn func(Line)) (cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextL
	s.nextL++
	s.listeners[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.listeners, id)
	}
}
