// Package protocol is the contract between orbit and its plugins: the manifest (orbit-plugin.yaml), the
// JSON-RPC 2.0 messages they exchange over the plugin's stdin / stdout (one JSON object per line) and the data of
// the widgets orbit draws on behalf of a plugin.
package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
)

// API is the version of this protocol; a plugin declares the one it was written for with `api:`.
const API = 1

// Requests orbit sends to a plugin (params → result).
const (
	MethodInitialize    = "initialize"       // InitializeParams → InitializeResult
	MethodShutdown      = "shutdown"         // → {}
	MethodViewData      = "view.data"        // ViewDataParams → ViewData
	MethodActionInvoke  = "action.invoke"    // InvokeParams → InvokeResult
	MethodActionOptions = "action.options"   // OptionsParams → []Option
	MethodSections      = "service.sections" // SectionsParams → []Section
	MethodServiceCheck  = "service.validate" // ServiceParams → {}
	MethodServiceStart  = "service.start"    // ServiceParams → {}
	MethodServiceStop   = "service.stop"     // ServiceParams → {}
	MethodServiceAttach = "service.attach"   // ServiceParams → AttachResult
)

// Notifications orbit sends to a plugin.
const (
	NotifyEvent = "event" // Event
)

// Notifications a plugin sends to orbit.
const (
	NotifyInvalidate = "ui.invalidate"  // Invalidate
	NotifyToast      = "ui.toast"       // Toast
	NotifyBadges     = "badges.set"     // BadgesSet
	NotifyServiceLog = "service.log"    // ServiceLog
	NotifyExited     = "service.exited" // ServiceExited
)

// JSON-RPC error codes.
const (
	ErrParse    = -32700
	ErrInvalid  = -32600
	ErrMethod   = -32601
	ErrParams   = -32602
	ErrInternal = -32603
	// ErrPlugin is what a plugin answers when the thing it was asked for failed; the message is shown to the user.
	ErrPlugin = -32000
)

// Error is the error member of a response.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// Message is any JSON-RPC message: a request, a response or a notification.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// IsRequest: it expects an answer.
func (m *Message) IsRequest() bool { return m.ID != nil && m.Method != "" }

// IsResponse: it answers a request of ours.
func (m *Message) IsResponse() bool { return m.ID != nil && m.Method == "" }

// Encode is the message as one line.
func Encode(m Message) []byte {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	return append(b, '\n')
}

// Notification is the line of a notification.
func Notification(method string, params any) []byte {
	raw, _ := json.Marshal(params)
	return Encode(Message{Method: method, Params: raw})
}

// Response is the line that answers request id with result.
func Response(id int64, result any) []byte {
	raw, _ := json.Marshal(result)
	if raw == nil || string(raw) == "null" {
		raw = []byte("{}")
	}
	return Encode(Message{ID: &id, Result: raw})
}

// Failure is the line that answers request id with an error.
func Failure(id int64, code int, msg string) []byte {
	return Encode(Message{ID: &id, Error: &Error{Code: code, Message: msg}})
}

// Request is the line of a request.
func Request(id int64, method string, params any) []byte {
	raw, _ := json.Marshal(params)
	return Encode(Message{ID: &id, Method: method, Params: raw})
}

// MaxLine is the longest message accepted, so a plugin that never ends a line cannot eat memory.
const MaxLine = 8 << 20

// Read calls onMessage for every JSON object line of r and onJunk for the other lines (a plugin that prints
// to stdout by mistake), until the stream ends.
func Read(r io.Reader, onMessage func(Message), onJunk func(line []byte)) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > MaxLine {
			line = nil
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			var m Message
			if trimmed[0] == '{' && json.Unmarshal(trimmed, &m) == nil && (m.Method != "" || m.ID != nil) {
				onMessage(m)
			} else if onJunk != nil {
				onJunk(trimmed)
			}
		}
		if err != nil {
			return
		}
	}
}
