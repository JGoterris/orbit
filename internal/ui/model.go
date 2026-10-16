package ui

import (
	"slices"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/console"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/projects"
	"github.com/JGoterris/orbit/internal/ui/graphlayout"
	"github.com/JGoterris/orbit/internal/ui/term"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

type mode int

const (
	modeNormal mode = iota
	modePalette
	modeFilter
	modeHelp
	modeEnv
	modeQuit
	modeStopping
	modeCopy
	modeTheme
	modeConsole
	modeReload
	modeProjects
	modeSwitch
)

// pane is a focusable panel.
type pane string

const (
	paneServices pane = "services"
	paneDetail   pane = "detail"
	paneLogs     pane = "logs"
	paneGraph    pane = "graph"
)

// viewDef is one of the screens, in header / number-key order.
type viewDef struct {
	id             string
	label          string
	panes          []pane
	defaultPane    pane
	compactSidebar bool
}

var views = []viewDef{
	{id: "dashboard", label: "Dashboard", panes: []pane{paneServices, paneDetail, paneLogs}, defaultPane: paneServices},
	{id: "graph", label: "Graph", panes: []pane{paneServices, paneGraph}, defaultPane: paneGraph, compactSidebar: true},
	{id: "logs", label: "Logs", panes: []pane{paneServices, paneLogs}, defaultPane: paneLogs},
	{id: "git", label: "Git", panes: []pane{paneRepos, paneChanges, paneBranches, paneCommits, paneStash, paneGitDiff, paneGitLog}, defaultPane: paneRepos},
}

func viewByID(id string) viewDef {
	for _, v := range views {
		if v.id == id {
			return v
		}
	}
	return views[0]
}

func (v viewDef) has(p pane) bool { return slices.Contains(v.panes, p) }

const (
	minSidebar    = 16
	minDetail     = 3
	defaultDetail = 9
)

// Options of New.
type Options struct {
	Sup supervisor.Like
	// OnQuit stops or leaves the services ("stop" | "detach"); it blocks until that is done, and runs off the UI loop.
	OnQuit func(how string)
	// Themes are every selectable theme: the built-in ones first, then the user's. Nil means the built-in ones.
	Themes map[string]theme.Palette
	// ThemeOrder is the order they are offered in
	ThemeOrder []string
	// CustomThemes are the names in Themes that come from the user's themes dir
	CustomThemes []string
	// InitialTheme is applied before the first render
	InitialTheme string
	// ThemeErrors are problems found while loading user themes, shown once as a toast
	ThemeErrors []string
	// Relaunch is an orbit.yaml that differs from what the running orbit has: offered as soon as the app opens
	Relaunch *Relaunch
	// SaveTheme persists the chosen theme; nil means it is not saved
	SaveTheme func(name string) bool
	// SetNotifications persists desktop notifications on / off and says whether it worked; Notifications reads it
	SetNotifications func(on bool) bool
	Notifications    func() bool
	// Started gets the program once it exists: the owner can ask it to quit from the outside
	Started func(p *tea.Program)
	// OpenProject switches to the project in dir ("stop" or "detach" the services of the current one). It blocks
	// and runs off the UI loop; on error nothing changed. Nil means switching is not available.
	OpenProject func(dir, how string) (supervisor.Like, *Relaunch, error)
	// Rebind is called with the new supervisor after a switch, so the owner can follow its events
	Rebind func(sup supervisor.Like)
	// StartWithPicker opens the project picker right away (orbit was started in a folder that is not a project)
	StartWithPicker bool
	// Splash plays the animated logo before the app shows up
	Splash bool
	// Version is orbit's, shown on the splash
	Version string
}

// Relaunch is an orbit.yaml on disk that differs from what the running orbit has.
type Relaunch struct {
	Diff   config.ConfigDiff
	Config *config.OrbitConfig
}

type themeEntry struct {
	name    string
	palette theme.Palette
	custom  bool
}

type toast struct {
	id    int
	text  string
	color string
}

type reloadOffer struct {
	diff config.ConfigDiff
	cfg  *config.OrbitConfig
}

type consoleState struct {
	name    string
	session *console.Session
	term    *term.Terminal
	detach  func()
	cols    int
	rows    int
}

// geometry is what the last render measured: the mouse and the scroll keys need it between frames.
type geometry struct {
	mu sync.Mutex
	geoData
}

// geoData is the copyable part of geometry.
type geoData struct {
	// service rows: y of the first row, how many, and the index of the first service shown
	listX, listY, listW, listH int
	listFirst                  int
	// log panel: its origin and the line (seq) at each row; maxBack is how far it can scroll
	logX, logY, logW, logH int
	logRows                []int
	maxBack                int
	// graph panel
	graphX, graphY, graphW, graphH int
	graphPadX, graphPadY           int
	graphOffX, graphOffY           int
	// header tabs: x ranges
	tabs []tabHit
	// clickable panels of the last render (the git view and the service detail)
	hits []hitRegion
	// log window of the last render: indexes into the displayed lines
	first, end int
}

// hitRegion is a panel the mouse can focus: its box, and for lists the first content row and the item shown there.
type hitRegion struct {
	x, y, w, h int
	pane       pane
	items      int // rows the list holds (0: not a list)
	first      int
}

// hit records a clickable panel while rendering; the content rows start under the top border.
func (m *Model) hit(p pane, x, y, w, h, items, first int) {
	m.geo.mu.Lock()
	m.geo.hits = append(m.geo.hits, hitRegion{x: x, y: y, w: w, h: h, pane: p, items: items, first: first})
	m.geo.mu.Unlock()
}

type tabHit struct {
	x0, x1 int
	view   string
}

// Model is the Bubble Tea model of the whole interface.
type Model struct {
	opts Options
	sup  supervisor.Like
	geo  *geometry
	// splash is the intro animation while it plays
	splash *splashState

	width, height int
	tick          int

	selected string
	view     string
	mode     mode
	focus    pane
	zoomed   bool

	resRange resources.Range
	logScope string // "selected" | "all"

	filter     string
	search     string
	bar        input
	barKind    string // "filter" | "search"
	scrollBack int
	showTime   bool
	wrap       bool
	fold       bool
	expanded   map[int]bool
	match      int // seq of the line n / N is on (0: none)

	copyCursor int // seq (0: none)
	copyAnchor int

	palette    input
	paletteIdx int

	envScroll  int
	envCursor  int // variable under the cursor
	envAnchor  int // where a range started (v), -1 for none
	helpScroll int
	envReveal  bool

	themeEntries []themeEntry
	themeIdx     int
	themeName    string
	savedTheme   string

	consoles *console.Manager
	console  *consoleState

	reload *reloadOffer

	toast   *toast
	toastID int

	sidebarDelta map[string]int
	detailH      int

	unread int
	seen   struct {
		key string
		seq int
	}

	layout    *graphlayout.Layout
	layoutKey int

	history    []resources.Bucket
	historyFor string
	historyAt  time.Time
	historyRng resources.Range

	projEntries  []projects.Entry
	projStatuses map[string]projects.Status
	projQuery    input
	projIdx      int
	pendingDir   string
	busyHow      string // "stop" | "detach": what the services are going through while modeStopping

	git        *gitState
	pendingGit bool

	lastStatus  map[string]supervisor.Status
	lastRes     map[string]resReport
	pendingSeen string

	quitting bool
}

// New builds the model for a supervisor.
func New(opts Options) *Model {
	themes := opts.Themes
	order := opts.ThemeOrder
	if themes == nil {
		themes = theme.Themes
		order = theme.Builtin
	}
	if len(order) == 0 {
		for n := range themes {
			order = append(order, n)
		}
		slices.Sort(order)
	}
	initial := opts.InitialTheme
	if _, ok := themes[initial]; !ok {
		initial = theme.DefaultName
	}
	theme.Apply(themes[initial])

	m := &Model{
		opts: opts, sup: opts.Sup, geo: &geometry{},
		width: 100, height: 30,
		view: views[0].id, focus: views[0].defaultPane,
		resRange: resources.Range2m, logScope: "all",
		wrap: true, fold: true, expanded: map[int]bool{}, barKind: "filter",
		sidebarDelta: map[string]int{},
		themeName:    initial, savedTheme: initial,
		consoles:   console.NewManager(),
		lastStatus: map[string]supervisor.Status{}, lastRes: map[string]resReport{},
	}
	for _, n := range order {
		m.themeEntries = append(m.themeEntries, themeEntry{name: n, palette: themes[n], custom: slices.Contains(opts.CustomThemes, n)})
	}
	if names := m.sup.Order(); len(names) > 0 {
		m.selected = names[0]
	}
	for _, n := range m.sup.Names() {
		st := m.sup.State(n)
		m.lastStatus[n] = st.Status
		m.lastRes[n] = resourceReport(st)
	}
	return m
}

// ---------------------------------------------------------------- messages

type tickMsg struct{}
type refreshMsg struct{}
type stateMsg struct{ name string }
type toastExpireMsg struct{ id int }
type quitDoneMsg struct{}
type copyDoneMsg struct {
	res  copyResult
	what string
	n    int
	unit string // what n counts; "line" when empty
}
type openURLMsg struct {
	url string
	ok  bool
}
type reloadDoneMsg struct {
	diff config.ConfigDiff
	err  error
}
type exportDoneMsg struct {
	path  string
	lines int
	err   error
}
type projectSwitchedMsg struct {
	sup      supervisor.Like
	relaunch *Relaunch
}
type projectSwitchFailedMsg struct{ err error }
type consoleExitMsg struct {
	name string
	code int
}
type historyMsg struct {
	name    string
	rng     resources.Range
	buckets []resources.Bucket
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

// ---------------------------------------------------------------- derived values

func (m *Model) names() []string { return m.sup.Order() }

// logService is the service the focused log panel shows; "" means all of them.
func (m *Model) logService() string {
	if m.view == "logs" && m.logScope == "all" {
		return ""
	}
	return m.selected
}

// rawViewLines are the lines that match the filter, stack traces included: what is copied, exported and searched.
func (m *Model) rawViewLines() []logs.Line {
	return logs.FilterLines(m.sup.LogStore().Lines(m.logService()), m.filter)
}

// viewLines are the rows the focused log panel shows (after the filter and the folding).
func (m *Model) viewLines() []logs.Line {
	raw := m.rawViewLines()
	if m.fold {
		return logs.FoldTraces(raw, m.expanded)
	}
	return raw
}

func (m *Model) viewDef() viewDef { return viewByID(m.view) }

func (m *Model) defaultSidebarW() int {
	if m.viewDef().compactSidebar {
		return min(30, max(22, m.width*18/100))
	}
	return min(52, max(40, m.width*30/100))
}

func (m *Model) clampSidebar(w int) int { return max(minSidebar, min(w, max(minSidebar, m.width-30))) }
func (m *Model) clampDetail(h int) int  { return max(minDetail, min(h, max(minDetail, m.height-10))) }

func (m *Model) sidebarW() int { return m.clampSidebar(m.defaultSidebarW() + m.sidebarDelta[m.view]) }

func (m *Model) detailRows() int {
	if m.detailH == 0 {
		return m.clampDetail(defaultDetail)
	}
	return m.clampDetail(m.detailH)
}

// pageSize is how many log rows a page key scrolls.
func (m *Model) pageSize() int {
	chrome := 5
	if m.zoomed {
		chrome = 4
	} else if m.view == "dashboard" {
		chrome = m.detailRows() + 3
	}
	return max(5, m.height-chrome)
}

func (m *Model) counts() (up, bad, busy int) {
	for _, n := range m.names() {
		switch m.sup.State(n).Status {
		case supervisor.Healthy, supervisor.Running:
			up++
		case supervisor.Crashed, supervisor.Failed, supervisor.Unhealthy:
			bad++
		case supervisor.Starting, supervisor.Waiting, supervisor.Stopping:
			busy++
		}
	}
	return
}

func resourceReport(st supervisor.ServiceState) resReport {
	r := st.Resources
	if r == nil {
		return resReport{}
	}
	return resReport{level: r.Level, leak: r.Leak != nil}
}
