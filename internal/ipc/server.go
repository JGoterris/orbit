package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/supervisor"
)

// outQueue is how many messages a client may have waiting; one that lets that many pile up unread is cut off
// instead of growing our memory.
const outQueue = 16384

type subscription struct {
	states   bool
	logs     bool
	services map[string]bool // nil: all of them
}

type serverConn struct {
	conn   net.Conn
	out    chan []byte
	once   sync.Once
	sub    subscription
	closed chan struct{}
}

func (c *serverConn) send(b []byte) {
	select {
	case <-c.closed:
	case c.out <- b:
	default:
		c.close() // too far behind
	}
}

func (c *serverConn) close() {
	c.once.Do(func() {
		close(c.closed)
		c.conn.Close()
	})
}

type rpcError struct {
	code int
	msg  string
}

func (e *rpcError) Error() string { return e.msg }

func paramsErr(format string, a ...any) error { return &rpcError{ErrParams, fmt.Sprintf(format, a...)} }

// Server exposes a supervisor on a local socket: queries, commands and a stream of state / log notifications.
type Server struct {
	sup        supervisor.Like
	path       string
	version    string
	onShutdown func(how string)

	mu       sync.Mutex
	listener net.Listener
	conns    map[*serverConn]struct{}
	cancels  []func()
}

// ServerOptions of NewServer.
type ServerOptions struct {
	// Version is what `hello` reports
	Version string
	// OnShutdown runs when a client asks to quit ("stop" or "detach"), after the request was answered
	OnShutdown func(how string)
}

func NewServer(sup supervisor.Like, opts ServerOptions) *Server {
	return &Server{
		sup: sup, path: SocketPath(sup.StateDir()), version: opts.Version, onShutdown: opts.OnShutdown,
		conns: map[*serverConn]struct{}{},
	}
}

// Path is the socket the server listens on.
func (s *Server) Path() string { return s.path }

// ClientCount is how many clients are connected.
func (s *Server) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Start begins listening. It returns false when another orbit already answers on this socket.
func (s *Server) Start() (bool, error) {
	if c, err := Connect(s.path, 500*time.Millisecond); err == nil {
		c.Close()
		return false, nil
	}
	if dir := socketDir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return false, err
		}
		os.Remove(s.path) // leftover of a process that did not clean up
	}
	ln, err := listen(s.path)
	if err != nil {
		return false, err
	}
	if socketDir(s.path) != "" {
		os.Chmod(s.path, 0o600)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	s.cancels = []func(){
		s.sup.OnChange(func(name string) {
			if name == "" {
				return
			}
			st := s.sup.State(name)
			s.broadcast(NotifyState, func(sub subscription) bool { return sub.states && wants(sub, name) },
				struct {
					Name  string                  `json:"name"`
					State supervisor.ServiceState `json:"state"`
				}{name, st})
		}),
		s.sup.OnConfig(func(diff config.ConfigDiff) {
			s.broadcast(NotifyConfig, func(sub subscription) bool { return sub.states },
				struct {
					Config *config.OrbitConfig `json:"config"`
					Diff   config.ConfigDiff   `json:"diff"`
				}{s.sup.Config(), diff})
			s.broadcast(NotifyConfigPending, func(sub subscription) bool { return sub.states },
				struct {
					Pending *supervisor.PendingConfig `json:"pending"`
				}{nil})
		}),
		s.sup.OnConfigPending(func(p *supervisor.PendingConfig) {
			s.broadcast(NotifyConfigPending, func(sub subscription) bool { return sub.states },
				struct {
					Pending *supervisor.PendingConfig `json:"pending"`
				}{p})
		}),
		s.sup.LogStore().OnLine(func(l logs.Line) {
			s.broadcast(NotifyLog, func(sub subscription) bool { return sub.logs && wants(sub, l.Service) }, l)
		}),
	}
	go s.acceptLoop(ln)
	return true, nil
}

func wants(sub subscription, service string) bool {
	return sub.services == nil || sub.services[service]
}

func (s *Server) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go s.serve(conn)
	}
}

// Close stops listening and hangs up on every client.
func (s *Server) Close() {
	s.mu.Lock()
	cancels := s.cancels
	s.cancels = nil
	ln := s.listener
	s.listener = nil
	conns := s.conns
	s.conns = map[*serverConn]struct{}{}
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	for c := range conns {
		c.close()
	}
	if ln != nil {
		ln.Close()
	}
	if socketDir(s.path) != "" {
		os.Remove(s.path)
	}
}

func (s *Server) broadcast(method string, pick func(subscription) bool, params any) {
	var msg []byte
	s.mu.Lock()
	targets := make([]*serverConn, 0, len(s.conns))
	for c := range s.conns {
		if pick(c.sub) {
			targets = append(targets, c)
		}
	}
	s.mu.Unlock()
	for _, c := range targets {
		if msg == nil {
			msg = notification(method, params)
		}
		c.send(msg)
	}
}

func (s *Server) serve(conn net.Conn) {
	c := &serverConn{conn: conn, out: make(chan []byte, outQueue), closed: make(chan struct{})}
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		c.close()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	go func() {
		for {
			select {
			case <-c.closed:
				return
			case b := <-c.out:
				if _, err := conn.Write(b); err != nil {
					c.close()
					return
				}
			}
		}
	}()
	reply := func(id *int, result any, err error) {
		if err != nil {
			code := ErrInternal
			if re, ok := err.(*rpcError); ok {
				code = re.code
			}
			c.send(encode(Message{ID: id, Error: &RPCError{Code: code, Message: err.Error()}}))
			return
		}
		if id == nil {
			return
		}
		raw, merr := json.Marshal(result)
		if merr != nil {
			c.send(encode(Message{ID: id, Error: &RPCError{Code: ErrInternal, Message: merr.Error()}}))
			return
		}
		c.send(encode(Message{ID: id, Result: raw}))
	}
	readMessages(conn, func(m Message) {
		if m.Method == "" {
			c.send(encode(Message{ID: m.ID, Error: &RPCError{Code: ErrInvalid, Message: "invalid request"}}))
			return
		}
		go func() {
			res, err := s.handle(c, m.Method, m.Params)
			reply(m.ID, res, err)
		}()
	}, func() {
		c.send(encode(Message{Error: &RPCError{Code: ErrParse, Message: "parse error"}}))
	})
}

type params struct {
	Services *[]string       `json:"services"`
	Service  *string         `json:"service"`
	SinceSeq int             `json:"sinceSeq"`
	Limit    *int            `json:"limit"`
	Since    int64           `json:"since"`
	Config   json.RawMessage `json:"config"`
	How      string          `json:"how"`
	States   *bool           `json:"states"`
	Logs     bool            `json:"logs"`
}

// targets are the service names of the params; groups expand to their members.
func (s *Server) targets(p params) ([]string, error) {
	if p.Services == nil {
		return nil, paramsErr("services must be a list of names")
	}
	cfg := s.sup.Config()
	var out []string
	seen := map[string]bool{}
	for _, n := range *p.Services {
		members := cfg.Groups[n]
		if members == nil {
			if cfg.Services[n] == nil {
				return nil, paramsErr("unknown service or group %q", n)
			}
			members = []string{n}
		}
		for _, m := range members {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out, nil
}

func (s *Server) service(p params) (string, error) {
	name := ""
	if p.Service != nil {
		name = *p.Service
	}
	if s.sup.Service(name) == nil {
		return "", paramsErr("unknown service %q", name)
	}
	return name, nil
}

func parallel(names []string, fn func(string) bool) bool {
	results := make([]bool, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = fn(n) }()
	}
	wg.Wait()
	for _, r := range results {
		if !r {
			return false
		}
	}
	return true
}

func (s *Server) handle(c *serverConn, method string, raw json.RawMessage) (any, error) {
	var p params
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, paramsErr("invalid params: %v", err)
		}
	}
	sup := s.sup
	ok := map[string]bool{"ok": true}
	switch method {
	case MethodHello:
		return Hello{Protocol: Protocol, Version: s.version, Pid: os.Getpid(), StateDir: sup.StateDir(), Config: sup.Config(), Pending: sup.Pending()}, nil
	case MethodSnapshot:
		return sup.Snapshot(), nil
	case MethodLogs:
		service := ""
		if p.Service != nil {
			var err error
			if service, err = s.service(p); err != nil {
				return nil, err
			}
		}
		lines := sup.LogStore().Lines(service)
		if p.SinceSeq > 0 {
			var fresh []logs.Line
			for _, l := range lines {
				if l.Seq > p.SinceSeq {
					fresh = append(fresh, l)
				}
			}
			lines = fresh
		}
		limit := 200
		if p.Limit != nil && *p.Limit >= 0 {
			limit = *p.Limit
		}
		if limit == 0 {
			return []logs.Line{}, nil
		}
		if len(lines) > limit {
			lines = lines[len(lines)-limit:]
		}
		if lines == nil {
			lines = []logs.Line{}
		}
		return lines, nil
	case MethodStart, MethodStop, MethodRestart, MethodToggle:
		names, err := s.targets(p)
		if err != nil {
			return nil, err
		}
		switch method {
		case MethodStart:
			return map[string]bool{"ok": parallel(names, sup.Start)}, nil
		case MethodRestart:
			return map[string]bool{"ok": parallel(names, sup.Restart)}, nil
		case MethodStop:
			parallel(names, func(n string) bool { sup.Stop(n, supervisor.StopOptions{}); return true })
		default:
			parallel(names, func(n string) bool { sup.Toggle(n); return true })
		}
		return ok, nil
	case MethodStartAll:
		sup.StartAll()
		return ok, nil
	case MethodStopAll:
		sup.StopAll()
		return ok, nil
	case MethodToggleWatch:
		name, err := s.service(p)
		if err != nil {
			return nil, err
		}
		var w any
		if r := sup.ToggleWatch(name); r != "" {
			w = r
		}
		return map[string]any{"watch": w}, nil
	case MethodClearLogs:
		service := ""
		if p.Service != nil {
			var err error
			if service, err = s.service(p); err != nil {
				return nil, err
			}
		}
		sup.ClearLogs(service)
		s.broadcast(NotifyCleared, func(subscription) bool { return true }, struct {
			Service string `json:"service,omitempty"`
		}{service})
		return ok, nil
	case MethodHistory:
		name, err := s.service(p)
		if err != nil {
			return nil, err
		}
		b, err := sup.HistoryOf(name, p.Since)
		if err != nil {
			return nil, paramsErr("%v", err)
		}
		return b, nil
	case MethodReload:
		var given *config.OrbitConfig
		if len(p.Config) > 0 && string(p.Config) != "null" {
			given = &config.OrbitConfig{}
			if err := json.Unmarshal(p.Config, given); err != nil || given.Services == nil {
				return nil, paramsErr("config must be an orbit config")
			}
		}
		diff, err := sup.Reload(given)
		if err != nil {
			return nil, paramsErr("%v", err) // a config that does not load: nothing was touched
		}
		return map[string]any{"ok": true, "diff": diff}, nil
	case MethodSubscribe:
		sub := subscription{states: p.States == nil || *p.States, logs: p.Logs}
		if p.Services != nil {
			names, err := s.targets(p)
			if err != nil {
				return nil, err
			}
			sub.services = map[string]bool{}
			for _, n := range names {
				sub.services[n] = true
			}
		}
		s.mu.Lock()
		c.sub = sub
		s.mu.Unlock()
		return map[string]any{"ok": true, "snapshot": sup.Snapshot()}, nil
	case MethodShutdown:
		how := "stop"
		if p.How == "detach" {
			how = "detach"
		}
		// answer first: the caller is waiting for the reply while we tear everything down
		if s.onShutdown != nil {
			time.AfterFunc(10*time.Millisecond, func() { s.onShutdown(how) })
		}
		return ok, nil
	}
	return nil, &rpcError{ErrMethod, fmt.Sprintf("unknown method %q", method)}
}
