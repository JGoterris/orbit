// Command demo is the plugin the tests of orbit run: it has something for every extension point.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JGoterris/orbit/pluginsdk"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := serve(); err != nil {
			fmt.Fprintln(os.Stderr, "demo:", err)
			os.Exit(1)
		}
		return
	}
	cli(os.Args[1:])
}

func cli(args []string) {
	switch {
	case len(args) > 0 && args[0] == "hello":
		fmt.Printf("hello from the demo plugin (project %s, args %s)\n", os.Getenv("ORBIT_PROJECT_DIR"), strings.Join(args[1:], " "))
	case len(args) > 0 && args[0] == "fail":
		fmt.Fprintln(os.Stderr, "demo: failing on purpose")
		os.Exit(3)
	default:
		fmt.Println("usage: orbit demo hello [args]")
	}
}

type state struct {
	mu       sync.Mutex
	versions map[string]int
	bumps    int
	services []string
}

func (s *state) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for n := range s.versions {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (s *state) version(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("0.%d.0", s.versions[name])
}

func (s *state) servicesSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.services...)
}

func serve() error {
	st := &state{versions: map[string]int{"alpha": 1, "beta": 2, "gamma": 3}}
	p := pluginsdk.New()
	cols := []protocol.Column{{ID: "name"}, {ID: "version", Priority: 1, Width: 8}}

	p.OnInit(func(i pluginsdk.Init) error {
		var settings struct{ Prefix string }
		if err := i.Settings(&settings); err != nil {
			return err
		}
		st.mu.Lock()
		for _, s := range i.Services {
			st.services = append(st.services, s.Name+"@"+s.Type)
		}
		st.mu.Unlock()
		p.SetHeaderBadges(protocol.Badge{Text: "demo", Tone: protocol.ToneAccent, Icon: "info", Action: "hello"})
		return nil
	})

	p.Pane("items", "list", func(r pluginsdk.Request) (protocol.ViewData, error) {
		var items []protocol.Item
		for _, n := range st.names() {
			items = append(items, pluginsdk.Row(n, "open", map[string]protocol.Cell{
				"name": pluginsdk.Cell(n), "version": pluginsdk.Cell(st.version(n), protocol.ToneMuted),
			}))
		}
		return pluginsdk.ListData(cols, items...), nil
	})
	p.Pane("items", "info", func(r pluginsdk.Request) (protocol.ViewData, error) {
		sel := r.Context.Selection["list"]
		return pluginsdk.DetailData(protocol.Section{
			Title:  "item " + sel,
			Fields: []protocol.Field{pluginsdk.Field("version", st.version(sel), protocol.ToneInfo), pluginsdk.Field("service", r.Context.Service), pluginsdk.Field("services", strings.Join(st.servicesSeen(), ","))},
		}), nil
	})
	p.Pane("items", "log", func(r pluginsdk.Request) (protocol.ViewData, error) {
		sel := r.Context.Selection["list"]
		return pluginsdk.TextData(
			pluginsdk.Line(pluginsdk.Span("log of "+sel, protocol.ToneAccent)),
			pluginsdk.Line(pluginsdk.Span("\x1b[31mnot red\x1b[0m", protocol.ToneError)),
		), nil
	})

	p.Action("bump", func(r pluginsdk.ActionRequest) (*protocol.InvokeResult, error) {
		sel := r.Context.Selection["list"]
		if sel == "" {
			return nil, fmt.Errorf("nothing selected")
		}
		st.mu.Lock()
		st.versions[sel]++
		st.bumps++
		n := st.bumps
		st.mu.Unlock()
		p.SetHeaderBadges(protocol.Badge{Text: fmt.Sprintf("bumps %d", n), Tone: protocol.ToneOK, Icon: "ok", Action: "hello"})
		return pluginsdk.Done(protocol.ToneOK, fmt.Sprintf("bumped %s (%s)", sel, r.Inputs["level"])), nil
	})
	p.Action("hello", func(r pluginsdk.ActionRequest) (*protocol.InvokeResult, error) {
		return pluginsdk.Done(protocol.ToneInfo, "hello from demo, service "+r.Context.Service, "none"), nil
	})

	p.Sections("info", func(service string) ([]protocol.Section, error) {
		return []protocol.Section{{Fields: []protocol.Field{pluginsdk.Field("demo says", "hi "+service, protocol.ToneAccent)}}}, nil
	})

	p.OnEvent(func(ev protocol.Event) {
		if ev.Kind != protocol.EventState {
			return
		}
		tone := protocol.ToneMuted
		switch ev.Status {
		case "healthy", "running":
			tone = protocol.ToneOK
		case "crashed", "failed":
			tone = protocol.ToneError
		}
		p.SetServiceBadges(ev.Service, protocol.Badge{Text: ev.Status, Tone: tone})
	})

	var tickers sync.Map
	p.ServiceType("ticker", pluginsdk.ServiceType{
		Validate: func(s pluginsdk.Service) error {
			var with struct{ IntervalMs int }
			if err := s.DecodeWith(&with); err != nil {
				return err
			}
			if with.IntervalMs < 0 {
				return fmt.Errorf("with.intervalMs must not be negative")
			}
			return nil
		},
		Start: func(s pluginsdk.Service) error {
			var with struct {
				Message    string
				IntervalMs int
				ExitAfter  int
			}
			if err := s.DecodeWith(&with); err != nil {
				return err
			}
			if with.Message == "" {
				with.Message = "tick"
			}
			if with.IntervalMs == 0 {
				with.IntervalMs = 100
			}
			stop := make(chan struct{})
			tickers.Store(s.Name, stop)
			p.Log(s.Name, "ticker started")
			go func() {
				for i := 1; ; i++ {
					select {
					case <-stop:
						return
					case <-time.After(time.Duration(with.IntervalMs) * time.Millisecond):
						p.Log(s.Name, fmt.Sprintf("%s #%d", with.Message, i))
						if with.ExitAfter > 0 && i >= with.ExitAfter {
							code := 0
							p.Exited(s.Name, &code, "")
							return
						}
					}
				}
			}()
			return nil
		},
		Stop: func(s pluginsdk.Service) error {
			if stop, ok := tickers.LoadAndDelete(s.Name); ok {
				close(stop.(chan struct{}))
			}
			return nil
		},
	})
	return p.Run()
}
