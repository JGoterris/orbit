package logs

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Backlog says how much of a file's end to replay when a tail starts.
type Backlog struct {
	Bytes int64
	Lines int
}

// FileTail follows a log file that another process appends to (poll based, survives truncation). It lets a service
// keep writing while orbit is closed and be re-read when it comes back.
type FileTail struct {
	path     string
	onLine   func(string)
	interval time.Duration

	mu      sync.Mutex // serialises reads: the ticker and Stop
	offset  int64
	pending string
	stop    chan struct{}
	done    chan struct{}
}

func NewFileTail(path string, onLine func(string)) *FileTail {
	return &FileTail{path: path, onLine: onLine, interval: 50 * time.Millisecond}
}

// Start begins following. With a backlog it first replays the end of the file and continues from there; without
// one it starts at the beginning.
func (t *FileTail) Start(backlog *Backlog) {
	if backlog != nil {
		if st, err := os.Stat(t.path); err == nil && st.Size() > 0 {
			for _, l := range ReadTail(t.path, backlog.Lines, backlog.Bytes) {
				t.onLine(l)
			}
			t.offset = st.Size()
		}
	}
	t.stop = make(chan struct{})
	t.done = make(chan struct{})
	go func() {
		defer close(t.done)
		tick := time.NewTicker(t.interval)
		defer tick.Stop()
		for {
			select {
			case <-t.stop:
				return
			case <-tick.C:
				t.read()
			}
		}
	}()
}

// StartAt begins following at offset, without replaying anything before it.
func (t *FileTail) StartAt(offset int64) {
	t.offset = offset
	t.Start(nil)
}

func (t *FileTail) read() {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	if st.Size() < t.offset {
		t.offset = 0 // truncated
	}
	if st.Size() == t.offset {
		return
	}
	buf := make([]byte, st.Size()-t.offset)
	n, _ := f.ReadAt(buf, t.offset)
	t.offset += int64(n)
	t.pending += string(buf[:n])
	for {
		nl := strings.IndexByte(t.pending, '\n')
		if nl == -1 {
			break
		}
		t.onLine(t.pending[:nl])
		t.pending = t.pending[nl+1:]
	}
}

// Stop ends the following. With drain it first reads what is left, including a last line without a newline.
func (t *FileTail) Stop(drain bool) {
	if t.stop == nil {
		return
	}
	select {
	case <-t.stop:
	default:
		close(t.stop)
	}
	<-t.done
	if !drain {
		return
	}
	t.read()
	t.mu.Lock()
	rest := t.pending
	t.pending = ""
	t.mu.Unlock()
	if rest != "" {
		t.onLine(rest)
	}
}

// PipeLines splits a byte stream into lines and calls onLine for each, until the stream ends.
func PipeLines(r io.Reader, onLine func(string)) {
	if r == nil {
		return
	}
	br := bufio.NewReaderSize(r, 32*1024)
	var buf strings.Builder
	for {
		chunk, err := br.ReadString('\n')
		buf.WriteString(chunk)
		if strings.HasSuffix(chunk, "\n") {
			line := strings.TrimSuffix(buf.String(), "\n")
			buf.Reset()
			onLine(line)
		} else if buf.Len() > 16_384 {
			// guard against output without newlines growing forever
			onLine(buf.String())
			buf.Reset()
		}
		if err != nil {
			break
		}
	}
	if buf.Len() > 0 {
		onLine(strings.ToValidUTF8(buf.String(), string(utf8.RuneError)))
	}
}
