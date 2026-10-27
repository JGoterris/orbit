package pluginsdk

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

type session struct {
	t     *testing.T
	in    *io.PipeWriter
	out   *io.PipeReader
	msgs  chan protocol.Message
	done  chan struct{}
	next  int64
	mu    sync.Mutex
	notes []protocol.Message
}

func start(t *testing.T, p *Plugin) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, out: outR, msgs: make(chan protocol.Message, 32), done: make(chan struct{})}
	go func() { p.Serve(inR, outW); outW.Close(); close(s.done) }()
	go protocol.Read(outR, func(m protocol.Message) {
		if m.IsResponse() {
			s.msgs <- m
			return
		}
		s.mu.Lock()
		s.notes = append(s.notes, m)
		s.mu.Unlock()
	}, nil)
	t.Cleanup(func() { inW.Close() })
	return s
}

func (s *session) call(method string, params any, out any) error {
	s.next++
	s.in.Write(protocol.Request(s.next, method, params))
	select {
	case m := <-s.msgs:
		if m.Error != nil {
			return m.Error
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-time.After(2 * time.Second):
		s.t.Fatalf("%s: no answer", method)
		return nil
	}
}

func (s *session) notifications() []protocol.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.Message(nil), s.notes...)
}

func TestPluginAnswersLikeOrbitAsksIt(t *testing.T) {
	p := New()
	var init Init
	var cfg struct{ Host string }
	p.OnInit(func(i Init) error { init = i; return i.Settings(&cfg) })
	p.Pane("prs", "list", func(r Request) (protocol.ViewData, error) {
		return ListData([]protocol.Column{{ID: "title"}}, Row(r.Context.Service, "ok", map[string]protocol.Cell{"title": Cell("hi", protocol.ToneOK)})), nil
	})
	p.Action("merge", func(r ActionRequest) (*protocol.InvokeResult, error) {
		return Done(protocol.ToneOK, "merged "+r.Inputs["method"]), nil
	})
	p.Action("quiet", func(ActionRequest) (*protocol.InvokeResult, error) { return nil, nil })
	p.Options("merge", "branch", func(OptionsRequest) ([]protocol.Option, error) {
		return []protocol.Option{{Value: "main"}}, nil
	})
	p.Sections("pr", func(service string) ([]protocol.Section, error) {
		return []protocol.Section{{Fields: []protocol.Field{Field("service", service)}}}, nil
	})
	s := start(t, p)

	if err := s.call(protocol.MethodInitialize, protocol.InitializeParams{API: 1, ProjectDir: "/p", Settings: json.RawMessage(`{"host":"gl.example"}`)}, nil); err != nil {
		t.Fatal(err)
	}
	if init.ProjectDir != "/p" || cfg.Host != "gl.example" {
		t.Fatalf("init = %+v settings = %+v", init, cfg)
	}

	var data protocol.ViewData
	if err := s.call(protocol.MethodViewData, protocol.ViewDataParams{View: "prs", Pane: "list", Context: protocol.Context{Service: "api"}}, &data); err != nil {
		t.Fatal(err)
	}
	if data.List == nil || data.List.Items[0].ID != "api" || data.List.Items[0].Cells["title"].Tone != protocol.ToneOK {
		t.Fatalf("data = %+v", data)
	}

	var res protocol.InvokeResult
	if err := s.call(protocol.MethodActionInvoke, protocol.InvokeParams{Action: "merge", Inputs: map[string]string{"method": "squash"}}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Toast == nil || res.Toast.Text != "merged squash" {
		t.Fatalf("result = %+v", res)
	}
	if err := s.call(protocol.MethodActionInvoke, protocol.InvokeParams{Action: "quiet"}, &res); err != nil {
		t.Fatalf("an action that returns nothing is fine: %v", err)
	}

	var opts []protocol.Option
	if err := s.call(protocol.MethodActionOptions, protocol.OptionsParams{Action: "merge", Input: "branch"}, &opts); err != nil || len(opts) != 1 {
		t.Fatalf("options = %v, %v", opts, err)
	}
	var secs []protocol.Section
	if err := s.call(protocol.MethodSections, protocol.SectionsParams{Service: "web", Section: "pr"}, &secs); err != nil || secs[0].Fields[0].Value != "web" {
		t.Fatalf("sections = %v, %v", secs, err)
	}
}

func TestErrorsReachOrbitAsErrors(t *testing.T) {
	p := New()
	p.Action("fail", func(ActionRequest) (*protocol.InvokeResult, error) { return nil, errors.New("gh: not logged in") })
	p.Action("panic", func(ActionRequest) (*protocol.InvokeResult, error) { panic("oops") })
	s := start(t, p)

	err := s.call(protocol.MethodActionInvoke, protocol.InvokeParams{Action: "fail"}, nil)
	var perr *protocol.Error
	if !errors.As(err, &perr) || perr.Code != protocol.ErrPlugin || perr.Message != "gh: not logged in" {
		t.Fatalf("a handler error must come back as it is: %v", err)
	}
	if err := s.call(protocol.MethodActionInvoke, protocol.InvokeParams{Action: "panic"}, nil); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("a panic must not take the plugin down: %v", err)
	}
	if err := s.call(protocol.MethodActionInvoke, protocol.InvokeParams{Action: "nope"}, nil); err == nil {
		t.Fatal("an action nobody answers for is an error")
	}
	if err := s.call("bogus", nil, nil); err == nil {
		t.Fatal("an unknown method is an error")
	}
	if err := s.call(protocol.MethodViewData, protocol.ViewDataParams{View: "x", Pane: "y"}, nil); err != nil && !strings.Contains(err.Error(), "no handler") {
		t.Fatalf("a pane nobody answers for must say so: %v", err)
	}
}

func TestRequestsAreAnsweredConcurrently(t *testing.T) {
	p := New()
	release := make(chan struct{})
	p.Action("slow", func(ActionRequest) (*protocol.InvokeResult, error) { <-release; return nil, nil })
	p.Action("fast", func(ActionRequest) (*protocol.InvokeResult, error) { return Done(protocol.ToneOK, "fast"), nil })
	s := start(t, p)
	s.next++
	s.in.Write(protocol.Request(100, protocol.MethodActionInvoke, protocol.InvokeParams{Action: "slow"}))
	// the fast one must not wait for the slow one
	if err := s.call(protocol.MethodActionInvoke, protocol.InvokeParams{Action: "fast"}, nil); err != nil {
		t.Fatal(err)
	}
	close(release)
}

func TestWhatThePluginTellsOrbit(t *testing.T) {
	p := New()
	s := start(t, p)
	// a plugin only talks once orbit has started it: after initialize, the stream is there
	if err := s.call(protocol.MethodInitialize, protocol.InitializeParams{API: 1}, nil); err != nil {
		t.Fatal(err)
	}
	p.Toast(protocol.ToneWarn, "careful")
	p.Invalidate("prs", "list")
	p.SetServiceBadges("api", protocol.Badge{Text: "!41"})
	p.SetHeaderBadges()
	p.Log("k", "line")
	deadline := time.Now().Add(2 * time.Second)
	for len(s.notifications()) < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	var methods []string
	for _, n := range s.notifications() {
		methods = append(methods, n.Method)
	}
	want := []string{protocol.NotifyToast, protocol.NotifyInvalidate, protocol.NotifyBadges, protocol.NotifyBadges, protocol.NotifyServiceLog}
	if strings.Join(methods, ",") != strings.Join(want, ",") {
		t.Fatalf("notifications = %v", methods)
	}
	var set protocol.BadgesSet
	json.Unmarshal(s.notifications()[2].Params, &set)
	if set.Target != "service:api" || set.Items[0].Text != "!41" {
		t.Fatalf("badges = %+v", set)
	}
}

func TestServiceTypes(t *testing.T) {
	p := New()
	var started, stopped []string
	p.ServiceType("k8s", ServiceType{
		Validate: func(s Service) error {
			var with struct{ Context string }
			if err := s.DecodeWith(&with); err != nil || with.Context == "" {
				return errors.New("with.context is required")
			}
			return nil
		},
		Start:  func(s Service) error { started = append(started, s.Name); return nil },
		Stop:   func(s Service) error { stopped = append(stopped, s.Name); return nil },
		Attach: func(s Service) (bool, error) { return s.Name == "up", nil },
	})
	s := start(t, p)
	svc := func(name, with string) protocol.ServiceParams {
		return protocol.ServiceParams{Name: name, Type: "k8s", With: json.RawMessage(with)}
	}
	if err := s.call(protocol.MethodServiceCheck, svc("a", `{}`), nil); err == nil || !strings.Contains(err.Error(), "with.context") {
		t.Fatalf("validate: %v", err)
	}
	if err := s.call(protocol.MethodServiceCheck, svc("a", `{"context":"prod"}`), nil); err != nil {
		t.Fatal(err)
	}
	s.call(protocol.MethodServiceStart, svc("a", `{}`), nil)
	s.call(protocol.MethodServiceStop, svc("a", `{}`), nil)
	var att protocol.AttachResult
	s.call(protocol.MethodServiceAttach, svc("up", `{}`), &att)
	if len(started) != 1 || len(stopped) != 1 || !att.Running {
		t.Fatalf("started %v stopped %v attach %+v", started, stopped, att)
	}
	if err := s.call(protocol.MethodServiceStart, protocol.ServiceParams{Name: "x", Type: "other"}, nil); err == nil {
		t.Fatal("a type the plugin does not provide must be refused")
	}
}

func TestShutdownReturnsEvenIfStdinStaysOpen(t *testing.T) {
	p := New()
	stopped := make(chan struct{})
	p.OnShutdown(func() { close(stopped) })
	s := start(t, p)
	if err := s.call(protocol.MethodShutdown, nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after shutdown")
	}
	<-stopped
}

func TestEventsReachThePlugin(t *testing.T) {
	p := New()
	got := make(chan protocol.Event, 1)
	p.OnEvent(func(e protocol.Event) { got <- e })
	s := start(t, p)
	s.in.Write(protocol.Notification(protocol.NotifyEvent, protocol.Event{Kind: protocol.EventState, Service: "api", Status: "running"}))
	select {
	case e := <-got:
		if e.Service != "api" || e.Status != "running" {
			t.Fatalf("event = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
}
