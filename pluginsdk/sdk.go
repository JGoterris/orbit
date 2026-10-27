// Package pluginsdk is the Go side of an orbit plugin: it speaks the protocol (see the protocol package) over the
// stdin and stdout of the process, so a plugin only writes the functions that answer.
//
//	p := pluginsdk.New()
//	p.Pane("prs", "list", func(r pluginsdk.Request) (protocol.ViewData, error) { … })
//	p.Action("merge", func(r pluginsdk.ActionRequest) (*protocol.InvokeResult, error) { … })
//	if err := p.Run(); err != nil { log.Fatal(err) }
//
// What the plugin contributes (views, actions, badges…) is declared in orbit-plugin.yaml; the handlers here answer
// for it. orbit draws everything: a plugin hands over data, never colours or escape sequences.
package pluginsdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

// Request is what a handler of a pane or of the sections is asked.
type Request struct {
	View    string
	Pane    string
	Context protocol.Context
}

// ActionRequest is what a handler of an action is asked: where the user was and what the dialog collected.
type ActionRequest struct {
	Action  string
	Context protocol.Context
	Inputs  map[string]string
}

// OptionsRequest is what a handler of a pick that gets its options from the plugin is asked.
type OptionsRequest struct {
	Action  string
	Input   string
	Context protocol.Context
}

// Service is a service of a type the plugin provides, as orbit.yaml declares it.
type Service struct {
	Name string
	Type string
	// With is the `with:` of the service, as JSON
	With          json.RawMessage
	Cwd           string
	StopTimeoutMs int64
}

// DecodeWith reads the `with:` of the service into v.
func (s Service) DecodeWith(v any) error {
	if len(s.With) == 0 {
		return nil
	}
	return json.Unmarshal(s.With, v)
}

// ServiceType is what a plugin does for the services of a type it provides. orbit keeps dependencies, health
// checks, restarts and logs; the plugin only starts and stops the thing.
type ServiceType struct {
	// Validate checks the `with:` of a service when orbit.yaml is loaded (optional)
	Validate func(Service) error
	// Start launches the service and returns once it is launched. Its output goes to Log; when it stops by itself,
	// call Exited.
	Start func(Service) error
	Stop  func(Service) error
	// Attach says whether the service of a previous session is still running (optional)
	Attach func(Service) (bool, error)
}

// Init is what the plugin is told when it starts.
type Init struct {
	OrbitVersion string
	Project      string
	// ProjectDir is the root of the project the plugin runs for ("" when there is none)
	ProjectDir string
	ConfigDir  string
	StateDir   string
	// Services are the services of the project (a config event brings them again if they change)
	Services []protocol.ServiceRef
	settings json.RawMessage
}

// Settings reads the settings the user and the project gave the plugin into v.
func (i Init) Settings(v any) error {
	if len(i.settings) == 0 {
		return nil
	}
	return json.Unmarshal(i.settings, v)
}

// Plugin is a plugin being written: register what it answers, then Run.
type Plugin struct {
	mu       sync.Mutex
	out      io.Writer
	panes    map[string]func(Request) (protocol.ViewData, error)
	actions  map[string]func(ActionRequest) (*protocol.InvokeResult, error)
	options  map[string]func(OptionsRequest) ([]protocol.Option, error)
	sections map[string]func(service string) ([]protocol.Section, error)
	services map[string]ServiceType
	onInit   func(Init) error
	onEvent  func(protocol.Event)
	onStop   func()
}

func New() *Plugin {
	return &Plugin{
		panes:    map[string]func(Request) (protocol.ViewData, error){},
		actions:  map[string]func(ActionRequest) (*protocol.InvokeResult, error){},
		options:  map[string]func(OptionsRequest) ([]protocol.Option, error){},
		sections: map[string]func(string) ([]protocol.Section, error){},
		services: map[string]ServiceType{},
	}
}

// OnInit runs when orbit starts the plugin; an error makes it fail to start.
func (p *Plugin) OnInit(fn func(Init) error) { p.onInit = fn }

// OnEvent gets the events the manifest asked for (state, config, log).
func (p *Plugin) OnEvent(fn func(protocol.Event)) { p.onEvent = fn }

// OnShutdown runs when orbit stops the plugin, before it exits.
func (p *Plugin) OnShutdown(fn func()) { p.onStop = fn }

// Pane answers for the pane `pane` of the view `view`.
func (p *Plugin) Pane(view, pane string, fn func(Request) (protocol.ViewData, error)) {
	p.panes[view+"/"+pane] = fn
}

// Action answers for an action of the manifest.
func (p *Plugin) Action(id string, fn func(ActionRequest) (*protocol.InvokeResult, error)) {
	p.actions[id] = fn
}

// Options answers for a pick of an action whose manifest says optionsFrom.
func (p *Plugin) Options(action, input string, fn func(OptionsRequest) ([]protocol.Option, error)) {
	p.options[action+"/"+input] = fn
}

// Sections answers for a block of the detail of a service (serviceSections of the manifest).
func (p *Plugin) Sections(id string, fn func(service string) ([]protocol.Section, error)) {
	p.sections[id] = fn
}

// ServiceType answers for a service type of the manifest.
func (p *Plugin) ServiceType(name string, t ServiceType) { p.services[name] = t }

func (p *Plugin) send(line []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.out != nil {
		p.out.Write(line)
	}
}

// Invalidate says that what a view (or one pane of it) shows is out of date: orbit asks again.
func (p *Plugin) Invalidate(view, pane string) {
	p.send(protocol.Notification(protocol.NotifyInvalidate, protocol.Invalidate{View: view, Pane: pane}))
}

// Toast shows a message in the footer of orbit.
func (p *Plugin) Toast(tone protocol.Tone, text string) {
	p.send(protocol.Notification(protocol.NotifyToast, protocol.Toast{Text: text, Tone: tone}))
}

// SetServiceBadges replaces the badges shown for a service (the column of the list and the detail). No badges
// removes them.
func (p *Plugin) SetServiceBadges(service string, badges ...protocol.Badge) {
	p.send(protocol.Notification(protocol.NotifyBadges, protocol.BadgesSet{Target: "service:" + service, Items: badges}))
}

// SetHeaderBadges replaces the badges the plugin shows in the header.
func (p *Plugin) SetHeaderBadges(badges ...protocol.Badge) {
	p.send(protocol.Notification(protocol.NotifyBadges, protocol.BadgesSet{Target: "header", Items: badges}))
}

// Log gives orbit a line of output of a service of the plugin.
func (p *Plugin) Log(service, text string) {
	p.send(protocol.Notification(protocol.NotifyServiceLog, protocol.ServiceLog{Name: service, Text: text}))
}

// LogError is Log for a line of the error output.
func (p *Plugin) LogError(service, text string) {
	p.send(protocol.Notification(protocol.NotifyServiceLog, protocol.ServiceLog{Name: service, Stream: "stderr", Text: text}))
}

// Exited says that a service of the plugin stopped by itself. code may be nil.
func (p *Plugin) Exited(service string, code *int, reason string) {
	p.send(protocol.Notification(protocol.NotifyExited, protocol.ServiceExited{Name: service, Code: code, Reason: reason}))
}

// Run serves orbit until it is told to shut down or stdin closes. It takes over os.Stdout for the protocol: what
// the plugin prints with fmt goes to stderr, which orbit writes to the log of the plugin.
func (p *Plugin) Run() error { return p.Serve(os.Stdin, os.Stdout) }

// Serve is Run over any pair of streams (tests).
func (p *Plugin) Serve(in io.Reader, out io.Writer) error {
	if f, ok := out.(*os.File); ok && f == os.Stdout {
		os.Stdout = os.Stderr
	}
	p.mu.Lock()
	p.out = out
	p.mu.Unlock()

	stop := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		protocol.Read(in, func(m protocol.Message) {
			if !m.IsRequest() {
				if m.Method == protocol.NotifyEvent && p.onEvent != nil {
					var ev protocol.Event
					if json.Unmarshal(m.Params, &ev) == nil {
						go p.onEvent(ev)
					}
				}
				return
			}
			if m.Method == protocol.MethodShutdown {
				p.send(protocol.Response(*m.ID, struct{}{}))
				once.Do(func() { close(stop) })
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				p.answer(m)
			}()
		}, func(line []byte) {
			fmt.Fprintf(os.Stderr, "pluginsdk: ignoring a line that is not a message: %.80s\n", line)
		})
	}()
	select {
	case <-stop:
	case <-readDone:
	}
	// what is still being answered gets a moment: orbit does not wait for a plugin that is slow to stop
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(time.Second):
	}
	if p.onStop != nil {
		p.onStop()
	}
	return nil
}

func (p *Plugin) answer(m protocol.Message) {
	id := *m.ID
	defer func() {
		if r := recover(); r != nil {
			p.send(protocol.Failure(id, protocol.ErrInternal, fmt.Sprintf("the plugin panicked: %v", r)))
		}
	}()
	result, err := p.dispatch(m)
	var perr *protocol.Error
	switch {
	case errors.As(err, &perr):
		p.send(protocol.Failure(id, perr.Code, perr.Message))
	case err != nil:
		p.send(protocol.Failure(id, protocol.ErrPlugin, err.Error()))
	default:
		p.send(protocol.Response(id, result))
	}
}

func badParams(err error) error {
	return &protocol.Error{Code: protocol.ErrParams, Message: "invalid params: " + err.Error()}
}

func (p *Plugin) dispatch(m protocol.Message) (any, error) {
	switch m.Method {
	case protocol.MethodInitialize:
		var in protocol.InitializeParams
		if err := json.Unmarshal(m.Params, &in); err != nil {
			return nil, badParams(err)
		}
		if p.onInit != nil {
			init := Init{OrbitVersion: in.OrbitVersion, Project: in.Project, ProjectDir: in.ProjectDir, ConfigDir: in.ConfigDir,
				StateDir: in.StateDir, Services: in.Services, settings: in.Settings}
			if err := p.onInit(init); err != nil {
				return nil, err
			}
		}
		return protocol.InitializeResult{}, nil
	case protocol.MethodViewData:
		var in protocol.ViewDataParams
		if err := json.Unmarshal(m.Params, &in); err != nil {
			return nil, badParams(err)
		}
		fn := p.panes[in.View+"/"+in.Pane]
		if fn == nil {
			return nil, fmt.Errorf("no handler for the pane %s/%s", in.View, in.Pane)
		}
		return fn(Request{View: in.View, Pane: in.Pane, Context: in.Context})
	case protocol.MethodActionInvoke:
		var in protocol.InvokeParams
		if err := json.Unmarshal(m.Params, &in); err != nil {
			return nil, badParams(err)
		}
		fn := p.actions[in.Action]
		if fn == nil {
			return nil, fmt.Errorf("no handler for the action %s", in.Action)
		}
		res, err := fn(ActionRequest{Action: in.Action, Context: in.Context, Inputs: in.Inputs})
		if res == nil {
			res = &protocol.InvokeResult{}
		}
		return res, err
	case protocol.MethodActionOptions:
		var in protocol.OptionsParams
		if err := json.Unmarshal(m.Params, &in); err != nil {
			return nil, badParams(err)
		}
		fn := p.options[in.Action+"/"+in.Input]
		if fn == nil {
			return nil, fmt.Errorf("no handler for the options %s/%s", in.Action, in.Input)
		}
		return fn(OptionsRequest{Action: in.Action, Input: in.Input, Context: in.Context})
	case protocol.MethodSections:
		var in protocol.SectionsParams
		if err := json.Unmarshal(m.Params, &in); err != nil {
			return nil, badParams(err)
		}
		fn := p.sections[in.Section]
		if fn == nil {
			return nil, fmt.Errorf("no handler for the section %s", in.Section)
		}
		return fn(in.Service)
	case protocol.MethodServiceCheck, protocol.MethodServiceStart, protocol.MethodServiceStop, protocol.MethodServiceAttach:
		return p.service(m)
	}
	return nil, &protocol.Error{Code: protocol.ErrMethod, Message: "unknown method " + m.Method}
}

func (p *Plugin) service(m protocol.Message) (any, error) {
	var in protocol.ServiceParams
	if err := json.Unmarshal(m.Params, &in); err != nil {
		return nil, badParams(err)
	}
	t, ok := p.services[in.Type]
	if !ok {
		return nil, fmt.Errorf("the plugin does not provide the service type %q", in.Type)
	}
	svc := Service{Name: in.Name, Type: in.Type, With: in.With, Cwd: in.Cwd, StopTimeoutMs: in.StopTimeoutMs}
	switch m.Method {
	case protocol.MethodServiceCheck:
		if t.Validate != nil {
			return struct{}{}, t.Validate(svc)
		}
		return struct{}{}, nil
	case protocol.MethodServiceStart:
		if t.Start == nil {
			return nil, fmt.Errorf("the service type %q cannot be started", in.Type)
		}
		return struct{}{}, t.Start(svc)
	case protocol.MethodServiceStop:
		if t.Stop == nil {
			return struct{}{}, nil
		}
		return struct{}{}, t.Stop(svc)
	default:
		if t.Attach == nil {
			return protocol.AttachResult{}, nil
		}
		running, err := t.Attach(svc)
		return protocol.AttachResult{Running: running}, err
	}
}

// ListData is the answer of a pane that is a list.
func ListData(columns []protocol.Column, items ...protocol.Item) protocol.ViewData {
	return protocol.ViewData{List: &protocol.ListData{Columns: columns, Items: items}}
}

// EmptyList is the answer of a list with nothing in it, and the sentence to show.
func EmptyList(message string) protocol.ViewData {
	return protocol.ViewData{List: &protocol.ListData{Items: []protocol.Item{}, Empty: message}}
}

// DetailData is the answer of a pane that is a detail.
func DetailData(sections ...protocol.Section) protocol.ViewData {
	return protocol.ViewData{Detail: &protocol.DetailData{Sections: sections}}
}

// TextData is the answer of a pane that is text.
func TextData(lines ...[]protocol.Span) protocol.ViewData {
	return protocol.ViewData{Text: &protocol.TextData{Lines: lines}}
}

// Cell is the text of a column of a row, with a tone.
func Cell(text string, tone ...protocol.Tone) protocol.Cell {
	c := protocol.Cell{Text: text}
	if len(tone) > 0 {
		c.Tone = tone[0]
	}
	return c
}

// Row is an item with the cells of its columns.
func Row(id string, icon string, cells map[string]protocol.Cell, badges ...protocol.Badge) protocol.Item {
	return protocol.Item{ID: id, Icon: icon, Cells: cells, Badges: badges}
}

func Line(spans ...protocol.Span) []protocol.Span { return spans }

// Span is a piece of text with a tone.
func Span(text string, tone ...protocol.Tone) protocol.Span {
	s := protocol.Span{Text: text}
	if len(tone) > 0 {
		s.Tone = tone[0]
	}
	return s
}

// Field is a label and its value.
func Field(label, value string, tone ...protocol.Tone) protocol.Field {
	f := protocol.Field{Label: label, Value: value}
	if len(tone) > 0 {
		f.Tone = tone[0]
	}
	return f
}

// Done is the result of an action that went well: a message, and the views to reload ("view" or "view/pane"; none
// reloads all of the plugin's; "none" reloads nothing).
func Done(tone protocol.Tone, text string, invalidate ...string) *protocol.InvokeResult {
	return &protocol.InvokeResult{Toast: &protocol.Toast{Text: text, Tone: tone}, Invalidate: invalidate}
}
