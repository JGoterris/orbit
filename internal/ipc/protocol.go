// Package ipc lets other processes talk to a running orbit: a JSON-RPC 2.0 server over a local socket (one
// message per line), a client, and a supervisor that mirrors a remote one, which is what the TUI becomes when the
// services run in an `orbit daemon`.
package ipc

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/supervisor"
)

// Protocol is bumped when a request or notification changes incompatibly.
const Protocol = 4

// Requests (params → result):
//
//	hello                                  → Hello
//	snapshot                               → []ServiceState
//	logs {service?, limit?, sinceSeq?}     → []Line
//	start|stop|restart|toggle {services}   → {ok}   (groups are expanded)
//	startAll | stopAll                     → {ok}
//	toggleWatch {service}                  → {watch: "paused"|"active"|null}
//	clearLogs {service?}                   → {ok}
//	history {service, since?}              → []Bucket   cpu / memory in 10 s buckets, `since` = ms back
//	reload {config?}                       → {ok, diff}  applies orbit.yaml (re-read by the server unless `config` is given)
//	subscribe {states?, logs?, services?}  → {ok, snapshot}  every notification after it is newer than the snapshot
//	shutdown {how: "stop"|"detach"}        → {ok}      the server exits afterwards
//
// Notifications (server → client): state {name, state}, log Line, cleared {service?},
// config {config, diff} (a reload happened), configPending {pending} (orbit.yaml changed on disk, not applied).
const (
	MethodHello       = "hello"
	MethodSnapshot    = "snapshot"
	MethodLogs        = "logs"
	MethodStart       = "start"
	MethodStop        = "stop"
	MethodRestart     = "restart"
	MethodToggle      = "toggle"
	MethodStartAll    = "startAll"
	MethodStopAll     = "stopAll"
	MethodToggleWatch = "toggleWatch"
	MethodClearLogs   = "clearLogs"
	MethodHistory     = "history"
	MethodReload      = "reload"
	MethodSubscribe   = "subscribe"
	MethodShutdown    = "shutdown"
)

// Notification methods.
const (
	NotifyState         = "state"
	NotifyLog           = "log"
	NotifyCleared       = "cleared"
	NotifyConfig        = "config"
	NotifyConfigPending = "configPending"
)

// JSON-RPC error codes.
const (
	ErrParse    = -32700
	ErrInvalid  = -32600
	ErrMethod   = -32601
	ErrParams   = -32602
	ErrInternal = -32603
)

// Hello is the answer to the first request: who is serving and what.
type Hello struct {
	Protocol int                 `json:"protocol"`
	Version  string              `json:"version"`
	Pid      int                 `json:"pid"`
	StateDir string              `json:"stateDir"`
	Config   *config.OrbitConfig `json:"config"`
	// Pending: orbit.yaml changed on disk and nobody applied it yet
	Pending *supervisor.PendingConfig `json:"pending,omitempty"`
}

// RPCError is the error member of a response.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// Message is any JSON-RPC message: a request, a response or a notification.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// IsResponse: it answers a request of ours.
func (m *Message) IsResponse() bool {
	return m.ID != nil && m.Method == "" && (m.Result != nil || m.Error != nil)
}

func encode(m Message) []byte {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	return append(b, '\n')
}

func notification(method string, params any) []byte {
	raw, _ := json.Marshal(params)
	return encode(Message{Method: method, Params: raw})
}

// readMessages calls onMessage for every JSON object line of r and onBad for the lines that are not one, until the
// stream ends.
func readMessages(r io.Reader, onMessage func(Message), onBad func()) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := trimSpace(line)
			if len(trimmed) > 0 {
				var m Message
				if trimmed[0] != '{' || json.Unmarshal(trimmed, &m) != nil {
					if onBad != nil {
						onBad()
					}
				} else {
					onMessage(m)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t' || b[len(b)-1] == '\r' || b[len(b)-1] == '\n') {
		b = b[:len(b)-1]
	}
	return b
}
