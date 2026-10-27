package protocol

import "encoding/json"

// Tone is a colour by role. Plugins never pick colours: orbit maps a tone to the active theme.
type Tone string

const (
	ToneDefault Tone = ""
	ToneOK      Tone = "ok"
	ToneWarn    Tone = "warn"
	ToneError   Tone = "error"
	ToneInfo    Tone = "info"
	ToneMuted   Tone = "muted"
	ToneAccent  Tone = "accent"
)

// ValidTone says whether t is a tone orbit knows, or a service colour ("service:<name>").
func ValidTone(t Tone) bool {
	switch t {
	case ToneDefault, ToneOK, ToneWarn, ToneError, ToneInfo, ToneMuted, ToneAccent:
		return true
	}
	return len(t) > len("service:") && t[:len("service:")] == "service:"
}

// Span is a piece of text with a tone.
type Span struct {
	Text string `json:"text"`
	Tone Tone   `json:"tone,omitempty"`
	Bold bool   `json:"bold,omitempty"`
}

// Badge is a short tag next to a service, in the header or in a list row. Action, when set, is the action that
// a click on it runs.
type Badge struct {
	Text   string `json:"text"`
	Tone   Tone   `json:"tone,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Action string `json:"action,omitempty"`
}

// Widget kinds.
const (
	WidgetList   = "list"
	WidgetDetail = "detail"
	WidgetText   = "text"
)

// Icons are the icon names a plugin may use in an item or a badge.
var Icons = []string{"ok", "fail", "run", "wait", "skip", "off", "warn", "info", "merge", "open", "closed", "draft"}

// Cell is the text of one column of a list row.
type Cell struct {
	Text string `json:"text"`
	Tone Tone   `json:"tone,omitempty"`
}

// Column is a column of a list, keyed by the id of the cells. The first column takes the space the others leave;
// of the rest, the ones with the highest Priority number are the first to be hidden when the panel is narrow.
// Width is in columns (0: as wide as its content, up to 24).
type Column struct {
	ID       string `json:"id"`
	Priority int    `json:"priority,omitempty"`
	Width    int    `json:"width,omitempty"`
}

// Item is a row of a list. ID identifies it between reloads, so the selection stays on it.
type Item struct {
	ID    string          `json:"id"`
	Cells map[string]Cell `json:"cells,omitempty"`
	// Icon is the name of one of orbit's icons (see Icons); anything else is not drawn.
	Icon   string  `json:"icon,omitempty"`
	Badges []Badge `json:"badges,omitempty"`
}

// ListData is the data of a list widget.
type ListData struct {
	Columns []Column `json:"columns,omitempty"`
	Items   []Item   `json:"items"`
	Empty   string   `json:"empty,omitempty"`
	Footer  string   `json:"footer,omitempty"`
}

// Field is a label and its value.
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Tone  Tone   `json:"tone,omitempty"`
}

// Section is a block of a detail: a title, label / value fields, free text and badges.
type Section struct {
	Title  string   `json:"title,omitempty"`
	Fields []Field  `json:"fields,omitempty"`
	Text   [][]Span `json:"text,omitempty"`
	Badges []Badge  `json:"badges,omitempty"`
}

// DetailData is the data of a detail widget.
type DetailData struct {
	Sections []Section `json:"sections"`
	Empty    string    `json:"empty,omitempty"`
}

// TextData is the data of a text widget: lines of spans, scrolled like the logs.
type TextData struct {
	Lines [][]Span `json:"lines"`
	Empty string   `json:"empty,omitempty"`
}

// ViewData is the answer to view.data: the one that matches the widget of the pane is used.
type ViewData struct {
	List   *ListData   `json:"list,omitempty"`
	Detail *DetailData `json:"detail,omitempty"`
	Text   *TextData   `json:"text,omitempty"`
}

// Context is what orbit tells a plugin about what the user is looking at.
type Context struct {
	// Service is the selected service ("" when none)
	Service string `json:"service,omitempty"`
	// Selection maps the id of a pane of the view to the id of its selected item
	Selection map[string]string `json:"selection,omitempty"`
	// Filter is what the user typed in the filter of the pane
	Filter string `json:"filter,omitempty"`
}

// ServiceRef is a service of the project: what a plugin needs to find where it lives.
type ServiceRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Cwd is the working directory of the service
	Cwd string `json:"cwd,omitempty"`
}

// InitializeParams is the first request a plugin gets.
type InitializeParams struct {
	API          int             `json:"api"`
	OrbitVersion string          `json:"orbitVersion"`
	Project      string          `json:"project,omitempty"`
	ProjectDir   string          `json:"projectDir,omitempty"`
	ConfigDir    string          `json:"configDir"`
	StateDir     string          `json:"stateDir"`
	Settings     json.RawMessage `json:"settings,omitempty"`
	// Services are the services of the project; a "config" event brings them again when they change
	Services []ServiceRef `json:"services,omitempty"`
}

// InitializeResult is the answer to initialize.
type InitializeResult struct {
	// Name and Version are informative, shown by `orbit plugin ls`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// ViewDataParams asks for the data of a pane.
type ViewDataParams struct {
	View    string  `json:"view"`
	Pane    string  `json:"pane"`
	Context Context `json:"context"`
}

// InvokeParams runs an action; Inputs are what its dialog collected.
type InvokeParams struct {
	Action  string            `json:"action"`
	Context Context           `json:"context"`
	Inputs  map[string]string `json:"inputs,omitempty"`
}

// InvokeResult is the answer to action.invoke.
type InvokeResult struct {
	Toast *Toast `json:"toast,omitempty"`
	// Invalidate makes orbit ask again for the data of these views ("view" or "view/pane"); "*" is all of them
	Invalidate []string `json:"invalidate,omitempty"`
}

// Option is an entry of a pick.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// OptionsParams asks for the entries of an input whose manifest says optionsFrom.
type OptionsParams struct {
	Action  string  `json:"action"`
	Input   string  `json:"input"`
	Context Context `json:"context"`
}

// SectionsParams asks for the blocks a plugin adds to the detail of a service.
type SectionsParams struct {
	Service string `json:"service"`
	Section string `json:"section"`
}

// ServiceParams is the request of a service of a type the plugin provides.
type ServiceParams struct {
	Name string          `json:"name"`
	Type string          `json:"type"`
	With json.RawMessage `json:"with,omitempty"`
	// Cwd is the working directory of the service
	Cwd string `json:"cwd,omitempty"`
	// StopTimeoutMs is how long service.stop may take
	StopTimeoutMs int64 `json:"stopTimeoutMs,omitempty"`
}

// AttachResult says whether the service from a previous session is still running.
type AttachResult struct {
	Running bool `json:"running"`
}

// Event is what a plugin that listed it in `events` is told.
type Event struct {
	Kind string `json:"kind"` // "state" | "config" | "log"
	// Service, Status and Ready for "state"
	Service string `json:"service,omitempty"`
	Status  string `json:"status,omitempty"`
	Ready   bool   `json:"ready,omitempty"`
	// Stream and Text for "log"
	Stream string `json:"stream,omitempty"`
	Text   string `json:"text,omitempty"`
	// Services for "config": the services of the project after the change
	Services []ServiceRef `json:"services,omitempty"`
}

// Event kinds.
const (
	EventState  = "state"
	EventConfig = "config"
	EventLog    = "log"
)

// Invalidate says that the data of a view (or one pane of it) is stale. An empty View means all of them.
type Invalidate struct {
	View string `json:"view,omitempty"`
	Pane string `json:"pane,omitempty"`
}

// Toast is a message in the footer of the TUI.
type Toast struct {
	Text string `json:"text"`
	Tone Tone   `json:"tone,omitempty"`
}

// BadgesSet replaces the badges a plugin shows at Target: "service:<name>" or "header".
type BadgesSet struct {
	Target string  `json:"target"`
	Items  []Badge `json:"items"`
}

// ServiceLog is a line of output of a service the plugin runs.
type ServiceLog struct {
	Name   string `json:"name"`
	Stream string `json:"stream,omitempty"` // "stdout" (default) | "stderr"
	Text   string `json:"text"`
}

// ServiceExited says a service the plugin runs stopped by itself. Code is nil when there is none.
type ServiceExited struct {
	Name   string `json:"name"`
	Code   *int   `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}
