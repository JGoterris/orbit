package logs

import (
	"regexp"
	"strings"
)

type TraceKind string

const (
	TraceJava   TraceKind = "java"
	TraceNode   TraceKind = "node"
	TracePython TraceKind = "python"
)

// TraceInfo is set on the first line of a stack trace (the "head"): what kind it is and how many lines follow it.
type TraceInfo struct {
	Kind   TraceKind `json:"kind"`
	Frames int       `json:"frames"`
	// Summary is, for python, the closing `ValueError: …` line, the only informative one once the trace is folded
	Summary string `json:"summary,omitempty"`
}

var (
	frameRe     = regexp.MustCompile(`^\s+at \S`)
	javaFrameRe = regexp.MustCompile(`^\s+at [\w$.<>/]+\(.*\)`)
	javaMoreRe  = regexp.MustCompile(`^\s+\.\.\. \d+ (?:more|common frames omitted)`)
	javaExtraRe = regexp.MustCompile(`^(?:Caused by: |\s+Suppressed: )`)
	pyHeadRe    = regexp.MustCompile(`^Traceback \(most recent call last\):`)
	pyChainRe   = regexp.MustCompile(`^(?:During handling of the above exception|The above exception was the direct cause)`)
	pyFinalRe   = regexp.MustCompile(`^[A-Za-z_][\w.]*(?:Error|Exception|Exit|Interrupt|Warning|Failure)(?::\s.*)?$|^[A-Za-z_][\w.]*: .*`)
	leadingWS   = regexp.MustCompile(`^\s`)
)

type traceState struct {
	// last is the previous line of this stream: the head of a Java/Node trace is only known once its first frame arrives
	last *Line
	head *Line
	// final: python's closing exception line was seen (only chaining text may follow)
	final bool
}

// TraceDetector finds Java / Node / Python stack traces in a line stream as it arrives and tags the lines (Trace on
// the head, TraceOf on the rest). State is per service and stream, so interleaved output does not mix.
type TraceDetector struct {
	states map[string]*traceState
}

func NewTraceDetector() *TraceDetector { return &TraceDetector{states: map[string]*traceState{}} }

func (d *TraceDetector) Feed(line *Line) {
	key := line.Service + "\x00" + string(line.Stream)
	s := d.states[key]
	if s == nil {
		s = &traceState{}
		d.states[key] = s
	}
	if line.Stream == System {
		s.head, s.last = nil, nil
		return
	}
	text := line.Text
	if s.head != nil && d.continues(s, s.head, text) {
		attach(s.head, line)
	} else {
		s.head = nil
		switch {
		case pyHeadRe.MatchString(text):
			line.Trace = &TraceInfo{Kind: TracePython}
			s.head = line
			s.final = false
		case frameRe.MatchString(text) && s.last != nil && s.last.Trace == nil && s.last.TraceOf == 0:
			s.head = s.last
			kind := TraceNode
			if javaFrameRe.MatchString(text) {
				kind = TraceJava
			}
			s.head.Trace = &TraceInfo{Kind: kind}
			attach(s.head, line)
		}
	}
	s.last = line
}

// Reset forgets the open traces of a service (or of all of them when it is "").
func (d *TraceDetector) Reset(service string) {
	if service == "" {
		d.states = map[string]*traceState{}
		return
	}
	for key := range d.states {
		if strings.HasPrefix(key, service+"\x00") {
			delete(d.states, key)
		}
	}
}

func attach(head, line *Line) {
	line.TraceOf = head.Seq
	head.Trace.Frames++
}

func (d *TraceDetector) continues(s *traceState, head *Line, text string) bool {
	trace := head.Trace
	if trace.Kind != TracePython {
		return frameRe.MatchString(text) || javaMoreRe.MatchString(text) || javaExtraRe.MatchString(text)
	}
	if strings.TrimSpace(text) != "" && leadingWS.MatchString(text) {
		return !s.final
	}
	if pyHeadRe.MatchString(text) {
		s.final = false
		return true
	}
	if text == "" || pyChainRe.MatchString(text) {
		return s.final
	}
	if !s.final && trace.Frames > 0 && pyFinalRe.MatchString(text) {
		s.final = true
		trace.Summary = text
		return true
	}
	return false
}
