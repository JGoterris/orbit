package logs

import "testing"

var (
	javaTrace = []string{
		`Exception in thread "main" java.lang.IllegalStateException: boom`,
		"\tat com.acme.Service.run(Service.java:42)",
		"\tat com.acme.Main.main(Main.java:10)",
		"Caused by: java.io.IOException: disk",
		"\tat com.acme.Io.read(Io.java:7)",
		"\t... 5 more",
	}
	nodeTrace = []string{
		"TypeError: Cannot read properties of null (reading 'x')",
		"    at Object.<anonymous> (/app/index.js:1:6)",
		"    at async Promise.all (index 0)",
		"    at node:internal/main:1:1",
	}
	pyTrace = []string{
		"Traceback (most recent call last):",
		`  File "a.py", line 3, in <module>`,
		"    1/0",
		"ZeroDivisionError: division by zero",
	}
)

func feed(s *Store, lines []string, service string, stream Stream) {
	for _, l := range lines {
		s.Append(service, stream, l)
	}
}

func TestJavaTraceHeadIsTheLineBeforeTheFirstFrame(t *testing.T) {
	s := NewStore(0, 0)
	feed(s, append(append([]string{"starting"}, javaTrace...), "after"), "api", Stderr)
	ls := s.Lines("")
	if ls[1].Trace == nil || ls[1].Trace.Kind != TraceJava || ls[1].Trace.Frames != 5 {
		t.Fatalf("head = %+v", ls[1].Trace)
	}
	for _, l := range ls[2:7] {
		if l.TraceOf != ls[1].Seq {
			t.Errorf("%q is not part of the trace", l.Text)
		}
	}
	if ls[0].Trace != nil || ls[7].TraceOf != 0 {
		t.Error("lines around the trace must stay untagged")
	}
}

func TestNodeTrace(t *testing.T) {
	s := NewStore(0, 0)
	feed(s, nodeTrace, "api", Stderr)
	if tr := s.Lines("")[0].Trace; tr == nil || tr.Kind != TraceNode || tr.Frames != 3 {
		t.Fatalf("trace = %+v", tr)
	}
}

func TestPythonTraceKeepsChainedTracebacksTogether(t *testing.T) {
	s := NewStore(0, 0)
	all := append([]string{}, pyTrace...)
	all = append(all, "", "During handling of the above exception, another exception occurred:", "")
	all = append(all, pyTrace...)
	all = append(all, "next")
	feed(s, all, "api", Stderr)
	ls := s.Lines("")
	if ls[0].Trace == nil || ls[0].Trace.Kind != TracePython || ls[0].Trace.Summary != "ZeroDivisionError: division by zero" {
		t.Fatalf("head = %+v", ls[0].Trace)
	}
	for _, l := range ls[1:11] {
		if l.TraceOf != ls[0].Seq {
			t.Errorf("%q should belong to the first trace", l.Text)
		}
	}
	if ls[11].TraceOf != 0 {
		t.Error("the line after the trace is not part of it")
	}
}

func TestInterleavedServicesDoNotMix(t *testing.T) {
	s := NewStore(0, 0)
	for i, l := range nodeTrace {
		s.Append("a", Stderr, l)
		s.Append("b", Stderr, "noise "+string(rune('0'+i)))
	}
	ls := s.Lines("")
	if ls[0].Trace == nil || ls[0].Trace.Frames != 3 {
		t.Fatalf("head = %+v", ls[0].Trace)
	}
	for _, l := range ls {
		if l.Service == "b" && (l.Trace != nil || l.TraceOf != 0) {
			t.Errorf("noise got tagged: %+v", l)
		}
	}
}

func TestFoldHidesFramesAndAMissingHeadKeepsThemVisible(t *testing.T) {
	s := NewStore(0, 0)
	feed(s, append(append([]string{"x"}, nodeTrace...), "y"), "api", Stderr)
	ls := s.Lines("")
	folded := FoldTraces(ls, map[int]bool{})
	if len(folded) != 3 || folded[1].Text != nodeTrace[0] {
		t.Fatalf("folded = %d lines", len(folded))
	}
	if got := FoldTraces(ls, map[int]bool{ls[1].Seq: true}); len(got) != len(ls) {
		t.Errorf("expanded = %d lines", len(got))
	}
	if got := FoldTraces(ls[2:], map[int]bool{}); len(got) != len(ls)-2 {
		t.Errorf("headless = %d lines", len(got))
	}
}

func TestOneFrameTraceIsNotFolded(t *testing.T) {
	s := NewStore(0, 0)
	feed(s, []string{"Error: x", "    at foo (a.js:1:1)", "done"}, "api", Stderr)
	if got := FoldTraces(s.Lines(""), map[int]bool{}); len(got) != 3 {
		t.Errorf("lines = %d", len(got))
	}
}

func TestWithFramesRestoresHiddenLines(t *testing.T) {
	s := NewStore(0, 0)
	feed(s, append(append([]string{}, pyTrace...), "z"), "api", Stderr)
	all := s.Lines("")
	got := WithFrames(FoldTraces(all, map[int]bool{}), all)
	if len(got) != len(pyTrace)+1 {
		t.Fatalf("lines = %d", len(got))
	}
}

func TestIngestDetectsAgain(t *testing.T) {
	src := NewStore(0, 0)
	feed(src, nodeTrace, "api", Stderr)
	dst := NewStore(0, 0)
	for _, l := range src.Lines("") {
		l.Seq = 999
		dst.Ingest(l)
	}
	ls := dst.Lines("")
	if ls[0].Trace == nil || ls[0].Trace.Frames != 3 || ls[1].TraceOf != ls[0].Seq {
		t.Fatalf("ingested = %+v", ls)
	}
}
