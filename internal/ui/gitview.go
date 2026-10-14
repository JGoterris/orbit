package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/git"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

const (
	paneRepos    pane = "repos"
	paneChanges  pane = "changes"
	paneBranches pane = "branches"
	paneCommits  pane = "commits"
	paneStash    pane = "stash"
	paneGitDiff  pane = "gitdiff"
	paneGitLog   pane = "gitlog"
)

var gitLists = []pane{paneChanges, paneBranches, paneCommits, paneStash}

func isGitList(p pane) bool {
	for _, l := range gitLists {
		if l == p {
			return true
		}
	}
	return false
}

type gitOutcome struct {
	ok   bool
	text string
}

type gitPrompt struct {
	kind  string // commit | branch | multiBranch | multiSwitch
	title string
	hint  string
	in    input
}

type gitConfirm struct {
	title   string
	message string
	run     func(m *Model) tea.Cmd
}

type gitSide struct {
	file string
	side git.DiffSide
}

// gitState is everything the Git view remembers between frames.
type gitState struct {
	entries  []git.Entry
	rootsKey string
	index    int
	sel      map[pane]int
	// logSel is the selected row of the Commands panel; -1 follows the newest
	logSel     int
	folded     map[string]bool
	marked     map[string]bool
	results    map[string]gitOutcome
	multi      bool
	sideChoice *gitSide
	split      bool
	scroll     int
	hunkIdx    int
	prompt     *gitPrompt
	confirm    *gitConfirm
	lastList   pane
	pageRows   int
	busy       atomic.Bool

	// diff: what is loaded, what was asked for, and a token that drops answers to stale requests
	diffKey   string
	patch     string
	rows      diffRows
	asked     string
	askedVer  int
	diffVer   int
	token     int
	mu        sync.Mutex
	lastFetch time.Time
	lastAll   time.Time
	cmdVer    int
}

func newGitState() *gitState {
	return &gitState{
		sel:      map[pane]int{},
		logSel:   -1,
		folded:   map[string]bool{},
		marked:   map[string]bool{},
		results:  map[string]gitOutcome{},
		lastList: paneChanges,
		pageRows: 20,
	}
}

// ---------------------------------------------------------------- messages

type gitDiffMsg struct {
	key   string
	token int
	patch string
}
type gitOpMsg struct {
	label string
	ok    string
	res   git.OpResult
}
type gitBatchMsg struct {
	verb, done string
	results    []git.RepoResult
}
type gitRefreshedMsg struct{}
type lazygitDoneMsg struct{ err error }

// ---------------------------------------------------------------- state access

// gs is the Git view's state, with its repos (re)discovered when the project's services changed.
func (m *Model) gs() *gitState {
	if m.git == nil {
		m.git = newGitState()
	}
	cfg := m.sup.Config()
	key := cfg.Root + "|" + strings.Join(m.sup.Names(), ",")
	for _, n := range m.sup.Names() {
		if s := m.sup.Service(n); s != nil {
			key += "|" + s.Cwd
		}
	}
	if m.git.rootsKey != key {
		m.git.rootsKey = key
		m.git.entries = git.Discover(cfg.Root, m.sup.Names(), func(n string) string {
			if s := m.sup.Service(n); s != nil {
				return s.Cwd
			}
			return cfg.Root
		})
		m.git.index = min(m.git.index, max(0, len(m.git.entries)-1))
		m.git.sel = map[pane]int{}
	}
	return m.git
}

func (g *gitState) current() (git.Entry, bool) {
	if len(g.entries) == 0 {
		return git.Entry{}, false
	}
	return g.entries[min(g.index, len(g.entries)-1)], true
}

// gitSel is the selection of one list, clamped to what it holds.
func (g *gitState) at(p pane, n int) int {
	if n == 0 {
		return 0
	}
	return max(0, min(g.sel[p], n-1))
}

type gitView struct {
	entry    git.Entry
	snap     git.Snapshot
	files    []git.FileChange
	tree     []git.TreeRow
	row      *git.TreeRow
	file     *git.FileChange
	dir      *git.TreeRow
	side     git.DiffSide
	branch   *git.BranchInfo
	commit   *git.CommitInfo
	stash    *git.StashInfo
	source   pane
	cmds     []git.CmdEntry
	logAt    int
	cmdEntry *git.CmdEntry
}

// view computes what is selected right now.
func (m *Model) gitView() (gitView, bool) {
	g := m.gs()
	entry, ok := g.current()
	if !ok {
		return gitView{}, false
	}
	v := gitView{entry: entry, snap: entry.Repo.Snapshot()}
	if v.snap.Status != nil {
		v.files = v.snap.Status.Files
	}
	v.tree = git.BuildTree(v.files, g.folded)
	if len(v.tree) > 0 {
		r := v.tree[g.at(paneChanges, len(v.tree))]
		v.row = &r
		if r.Dir {
			v.dir = &r
		} else {
			f := r.File
			v.file = &f
		}
	}
	v.side = git.Unstaged
	if v.file != nil && v.file.Kind != git.Untracked {
		switch {
		case g.sideChoice != nil && g.sideChoice.file == v.file.Path:
			v.side = g.sideChoice.side
		case v.file.HasUnstaged():
			v.side = git.Unstaged
		default:
			v.side = git.Staged
		}
	}
	if n := len(v.snap.Branches); n > 0 {
		v.branch = &v.snap.Branches[g.at(paneBranches, n)]
	}
	if n := len(v.snap.Commits); n > 0 {
		v.commit = &v.snap.Commits[g.at(paneCommits, n)]
	}
	if n := len(v.snap.Stashes); n > 0 {
		v.stash = &v.snap.Stashes[g.at(paneStash, n)]
	}
	v.source = g.lastList
	if isGitList(m.focus) {
		v.source = m.focus
	}
	roots := map[string]bool{}
	for _, e := range g.entries {
		roots[e.Repo.Root] = true
	}
	for _, c := range git.Commands.Entries() {
		if roots[c.Root] {
			v.cmds = append(v.cmds, c)
		}
	}
	if n := len(v.cmds); n > 0 {
		v.logAt = n - 1
		if g.logSel >= 0 {
			v.logAt = max(0, min(g.logSel, n-1))
		}
		c := v.cmds[v.logAt]
		v.cmdEntry = &c
	}
	return v, true
}

type gitTarget struct {
	key   string
	title string
	load  func() string
}

func (v gitView) target() *gitTarget {
	root := v.entry.Repo.Root
	switch v.source {
	case paneChanges:
		if v.file != nil {
			f, side := *v.file, v.side
			return &gitTarget{key: fmt.Sprintf("c:%s:%s:%c%c", f.Path, side, f.X, f.Y), title: fmt.Sprintf("%s · %s", f.Path, side),
				load: func() string { return git.FileDiff(root, f, side) }}
		}
	case paneBranches:
		if v.branch != nil {
			b := *v.branch
			if b.Current {
				return &gitTarget{key: "b:" + b.Name, title: b.Name + " (current)", load: func() string { return "" }}
			}
			return &gitTarget{key: "b:" + b.Name, title: "HEAD..." + b.Name, load: func() string { return git.RangeDiff(root, "HEAD..."+b.Name) }}
		}
	case paneCommits:
		if v.commit != nil {
			c := *v.commit
			return &gitTarget{key: "m:" + c.Hash, title: c.Hash + " " + c.Subject, load: func() string { return git.CommitDiff(root, c.Hash) }}
		}
	case paneStash:
		if v.stash != nil {
			s := *v.stash
			return &gitTarget{key: "s:" + s.Ref + s.Hash, title: s.Ref + " " + s.Message, load: func() string { return git.StashDiff(root, s.Hash) }}
		}
	}
	return nil
}

// gitSync asks for the diff of whatever is selected when it changed (or the repo did), dropping stale answers.
func (m *Model) gitSync() tea.Cmd {
	if m.view != "git" {
		return nil
	}
	v, ok := m.gitView()
	g := m.gs()
	if !ok {
		return nil
	}
	t := v.target()
	key := ""
	if t != nil {
		key = t.key
	}
	if key == g.asked && g.askedVer == g.diffVer+v.snap.Version {
		return nil
	}
	g.asked, g.askedVer = key, g.diffVer+v.snap.Version
	g.token++
	token := g.token
	if t == nil {
		g.diffKey, g.patch, g.rows = "", "", diffRows{}
		g.scroll, g.hunkIdx = 0, 0
		return nil
	}
	return func() tea.Msg { return gitDiffMsg{key: t.key, token: token, patch: t.load()} }
}

func (m *Model) gitOnDiff(msg gitDiffMsg) {
	g := m.gs()
	if msg.token != g.token {
		return // a slow answer for a previous selection
	}
	if g.diffKey != msg.key {
		g.scroll, g.hunkIdx = 0, 0
	}
	if g.diffKey == msg.key && g.patch == msg.patch {
		return
	}
	g.diffKey, g.patch = msg.key, msg.patch
	g.rows = buildRows(git.ParseDiff(msg.patch))
}

// gitRefresh reads the repos in the background: every one in full (the table shows last commit and stashes), or
// just the selected one.
func (m *Model) gitRefresh(all bool) tea.Cmd {
	g := m.gs()
	entries := g.entries
	cur, _ := g.current()
	return func() tea.Msg {
		if all {
			var wg sync.WaitGroup
			for _, e := range entries {
				wg.Add(1)
				go func() { defer wg.Done(); e.Repo.Refresh("all") }()
			}
			wg.Wait()
		} else if cur.Repo != nil {
			cur.Repo.Refresh("all")
		}
		return gitRefreshedMsg{}
	}
}

// gitTick is called every 250ms: the selected repo is read every 3 s, all of them every 10 s.
func (m *Model) gitTick() tea.Cmd {
	if m.view != "git" {
		return nil
	}
	g := m.gs()
	now := time.Now()
	var cmds []tea.Cmd
	if now.Sub(g.lastAll) > 10*time.Second {
		g.lastAll, g.lastFetch = now, now
		cmds = append(cmds, m.gitRefresh(true))
	} else if now.Sub(g.lastFetch) > 3*time.Second {
		g.lastFetch = now
		g.diffVer++
		cmds = append(cmds, m.gitRefresh(false))
	}
	if v := git.Commands.Version(); v != g.cmdVer {
		g.cmdVer = v
	}
	return tea.Batch(cmds...)
}

// gitEnter is called when the view opens: the diff and every repo are read right away.
func (m *Model) gitEnter(service string) tea.Cmd {
	g := m.gs()
	if i := git.RepoOfService(g.entries, service); i >= 0 {
		g.index = i
	}
	g.lastAll, g.lastFetch = time.Now(), time.Now()
	g.asked = ""
	return m.gitRefresh(true)
}

// ---------------------------------------------------------------- operations

func (m *Model) gitAct(label string, run func() git.OpResult, ok string) tea.Cmd {
	g := m.gs()
	entry, has := g.current()
	if !has {
		return nil
	}
	if !g.busy.CompareAndSwap(false, true) {
		return m.notify("another git operation is still running", theme.Active().Yellow)
	}
	return func() tea.Msg {
		defer g.busy.Store(false)
		res := entry.Repo.Run(run)
		return gitOpMsg{label: label, ok: ok, res: res}
	}
}

func (m *Model) gitBatch(verb, done string, on []git.Entry, op func(git.Entry) git.OpResult) tea.Cmd {
	g := m.gs()
	if !g.busy.CompareAndSwap(false, true) {
		return m.notify("another git operation is still running", theme.Active().Yellow)
	}
	return func() tea.Msg {
		defer g.busy.Store(false)
		return gitBatchMsg{verb: verb, done: done, results: git.RunOnRepos(on, op)}
	}
}

func (m *Model) gitOnOp(msg gitOpMsg) tea.Cmd {
	p := theme.Active()
	m.gs().diffVer++
	if msg.res.OK {
		text := msg.ok
		if text == "" {
			text = msg.label
		}
		return m.notify(text, p.Green)
	}
	return m.notify(fmt.Sprintf("%s failed: %s", msg.label, msg.res.Message), p.Red)
}

func (m *Model) gitOnBatch(msg gitBatchMsg) tea.Cmd {
	g := m.gs()
	g.diffVer++
	for _, r := range msg.results {
		text := msg.done
		if !r.Res.OK {
			text = r.Res.Message
		}
		g.results[r.Entry.Repo.Root] = gitOutcome{ok: r.Res.OK, text: text}
	}
	sum := git.Summarize(msg.verb, msg.results)
	p := theme.Active()
	if sum.OK {
		return m.notify(sum.Message, p.Green)
	}
	return m.notify(sum.Message, p.Red)
}

// gitTargets are the repos a multi-repo action touches: the marked ones, or all of them when none is marked.
func (m *Model) gitTargets() []git.Entry {
	g := m.gs()
	if len(g.marked) == 0 {
		return g.entries
	}
	var out []git.Entry
	for _, e := range g.entries {
		if g.marked[e.Repo.Root] {
			out = append(out, e)
		}
	}
	return out
}

func entryNames(on []git.Entry) string {
	names := make([]string, len(on))
	for i, e := range on {
		names[i] = e.Name
	}
	return strings.Join(names, ", ")
}

func pushOp(e git.Entry) git.OpResult {
	st := e.Repo.Snapshot().Status
	if st == nil || st.Branch == "" {
		return git.OpResult{Message: "detached HEAD: no branch to push"}
	}
	return git.Push(e.Repo.Root, st.Branch, st.Upstream != "")
}

// gitNetwork is f / p / u on the selected repo or, in multi-repo mode, on every target (pull and push ask first).
func (m *Model) gitNetwork(k string, many bool) tea.Cmd {
	g := m.gs()
	entry, ok := g.current()
	if !ok {
		return nil
	}
	var verb string
	var op func(git.Entry) git.OpResult
	switch k {
	case "f":
		verb, op = "fetched", func(e git.Entry) git.OpResult { return git.Fetch(e.Repo.Root) }
	case "p":
		verb, op = "pulled", func(e git.Entry) git.OpResult { return git.Pull(e.Repo.Root) }
	default:
		verb, op = "pushed", pushOp
	}
	on := []git.Entry{entry}
	if many {
		on = m.gitTargets()
	}
	run := func(m *Model) tea.Cmd { return m.gitBatch(verb, verb, on, op) }
	if !many || k == "f" {
		return run(m)
	}
	title := "Push"
	if k == "p" {
		title = "Pull"
	}
	g.confirm = &gitConfirm{title: fmt.Sprintf("%s %d repos", title, len(on)), message: entryNames(on), run: run}
	return nil
}

func (m *Model) gitNewBranch(name string, on []git.Entry, ask bool) tea.Cmd {
	run := func(m *Model) tea.Cmd {
		return m.gitBatch("new branch", "on "+name, on, func(e git.Entry) git.OpResult { return git.CreateBranch(e.Repo.Root, name) })
	}
	if !ask {
		return run(m)
	}
	m.gs().confirm = &gitConfirm{title: fmt.Sprintf("New branch %s in %d repos", name, len(on)), message: entryNames(on), run: run}
	return nil
}

func (m *Model) gitSwitchBranch(name string, on []git.Entry) tea.Cmd {
	m.gs().confirm = &gitConfirm{
		title: fmt.Sprintf("Switch %d repos to %s", len(on), name), message: entryNames(on),
		run: func(m *Model) tea.Cmd {
			return m.gitBatch("switched", "on "+name, on, func(e git.Entry) git.OpResult { return git.Checkout(e.Repo.Root, name) })
		},
	}
	return nil
}

func (m *Model) gitLazygit(root string) tea.Cmd {
	p := theme.Active()
	if _, err := exec.LookPath("lazygit"); err != nil {
		return m.notify("lazygit is not installed", p.Yellow)
	}
	m.consoles.CloseAll()
	c := exec.Command("lazygit", "-p", root)
	return tea.ExecProcess(c, func(err error) tea.Msg { return lazygitDoneMsg{err} })
}

// serviceLazygit is `L` outside the Git view: lazygit on the repo a service lives in.
func (m *Model) serviceLazygit(name string) tea.Cmd {
	svc := m.sup.Service(name)
	if svc == nil {
		return nil
	}
	if root := git.FindRoot(svc.Cwd); root != "" {
		return m.gitLazygit(root)
	}
	return m.notify(name+" is not in a git repository", theme.Active().Yellow)
}

func (m *Model) gitCopy(text string) tea.Cmd {
	return func() tea.Msg {
		r := clipboardCopy(text)
		return copyDoneMsg{res: r, what: text, n: 1}
	}
}

// ---------------------------------------------------------------- keys

// gitKeys handles a key in the Git view; handled says whether it was used (the shell then does not).
func (m *Model) gitKeys(k tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	g := m.gs()
	s := k.String()
	p := theme.Active()
	if g.confirm != nil {
		c := g.confirm
		switch s {
		case "y", "Y", "enter":
			g.confirm = nil
			return c.run(m), true
		case "n", "N", "esc":
			g.confirm = nil
		}
		return nil, true
	}
	if g.prompt != nil {
		switch s {
		case "esc":
			g.prompt = nil
		case "enter":
			return m.gitSubmit(), true
		default:
			g.prompt.in.handle(k)
		}
		return nil, true
	}
	// multi-repo mode: the next key says what to do on every target
	if g.multi {
		g.multi = false
		switch s {
		case "f", "p", "u":
			return m.gitNetwork(s, true), true
		case "b":
			m.gitOpenMulti("multiBranch")
		case "s":
			m.gitOpenMulti("multiSwitch")
		case "esc", "m":
		default:
			return m.notify("multi-repo: f fetch · p pull · u push · b new branch · s switch branch", p.Yellow), true
		}
		return nil, true
	}
	v, ok := m.gitView()
	if !ok {
		return nil, false
	}
	root := v.entry.Repo.Root
	down := s == "down" || s == "j"
	up := s == "up" || s == "k"

	// keys that work in every pane
	switch s {
	case "m":
		g.multi = true
		return nil, true
	case "f", "p", "u":
		return m.gitNetwork(s, false), true
	case "L":
		return m.gitLazygit(root), true
	}

	n := len(g.entries)
	switch m.focus {
	case paneGitDiff:
		page := g.pageRows
		switch s {
		case "esc":
			m.focus = g.lastList
		case "down", "j":
			g.scroll++
		case "up", "k":
			g.scroll = max(0, g.scroll-1)
		case "ctrl+d":
			g.scroll += page / 2
		case "ctrl+u":
			g.scroll = max(0, g.scroll-page/2)
		case "pgdown":
			g.scroll += page
		case "pgup":
			g.scroll = max(0, g.scroll-page)
		case "g", "home":
			g.scroll = 0
		case "G", "end":
			g.scroll = 1 << 30
		case "]", "[":
			if len(g.rows.hunks) == 0 {
				return nil, true
			}
			top := m.gitTop()
			cur := currentHunk(g.rows, g.hunkIdx, top, page)
			d := 1
			if s == "[" {
				d = -1
			}
			next := max(0, min(len(g.rows.hunks)-1, cur+d))
			g.hunkIdx = next
			g.scroll = revealHunk(g.rows, next, top, page)
		case "s":
			g.split = !g.split
			g.scroll = 0
		case "}", "{":
			if v.source != paneChanges {
				return nil, false
			}
			d := 1
			if s == "{" {
				d = -1
			}
			m.gitStepFile(v, d)
		case "y", "Y":
			if v.source == paneChanges && v.file != nil {
				return m.gitCopyPath(v, v.file.Path, s == "Y"), true
			}
			return nil, false
		default:
			return nil, false
		}
		return nil, true
	case paneRepos:
		switch {
		case down:
			g.index = min(n-1, g.index+1)
		case up:
			g.index = max(0, g.index-1)
		case s == "g" || s == "home":
			g.index = 0
		case s == "G" || s == "end":
			g.index = n - 1
		case s == " ":
			r := v.entry.Repo.Root
			if g.marked[r] {
				delete(g.marked, r)
			} else {
				g.marked[r] = true
			}
		case s == "a":
			if len(g.marked) == n {
				g.marked = map[string]bool{}
			} else {
				for _, e := range g.entries {
					g.marked[e.Repo.Root] = true
				}
			}
		case s == "enter":
			m.focus = paneChanges
		default:
			return nil, false
		}
		g.sel = map[pane]int{}
		return nil, true
	case paneGitLog:
		cnt := len(v.cmds)
		switch {
		case down:
			if v.logAt+1 >= cnt-1 {
				g.logSel = -1
			} else {
				g.logSel = v.logAt + 1
			}
		case up:
			g.logSel = max(0, v.logAt-1)
		case s == "g" || s == "home":
			g.logSel = 0
		case s == "G" || s == "end":
			g.logSel = -1
		case s == "c":
			git.Commands.Clear()
			g.logSel = -1
		case (s == "y" || s == "Y") && v.cmdEntry != nil:
			return m.gitCopy("git " + git.FormatArgs(v.cmdEntry.Args)), true
		default:
			return nil, false
		}
		return nil, true
	}
	if !isGitList(m.focus) {
		return nil, false
	}

	lens := map[pane]int{paneChanges: len(v.tree), paneBranches: len(v.snap.Branches), paneCommits: len(v.snap.Commits), paneStash: len(v.snap.Stashes)}
	move := func(to int) { g.sel[m.focus] = max(0, min(lens[m.focus]-1, to)) }
	cur := g.at(m.focus, lens[m.focus])
	switch {
	case down:
		move(cur + 1)
		return nil, true
	case up:
		move(cur - 1)
		return nil, true
	case s == "g" || s == "home":
		move(0)
		return nil, true
	case s == "G" || s == "end":
		move(lens[m.focus] - 1)
		return nil, true
	}

	hasCommits := v.snap.Status == nil || v.snap.Status.HasCommits
	switch m.focus {
	case paneChanges:
		switch s {
		case " ":
			if v.file != nil {
				return m.gitToggleFile(*v.file, root, hasCommits), true
			}
			if v.dir != nil {
				return m.gitToggleDir(*v.dir, root, hasCommits), true
			}
			return nil, true
		case "a":
			anyUnstaged := false
			for _, f := range v.files {
				anyUnstaged = anyUnstaged || f.HasUnstaged()
			}
			if anyUnstaged {
				return m.gitAct("stage all", func() git.OpResult { return git.StageAll(root) }, "staged everything"), true
			}
			return m.gitAct("unstage all", func() git.OpResult { return git.UnstageAll(root, hasCommits) }, "unstaged everything"), true
		case "c":
			staged := false
			for _, f := range v.files {
				staged = staged || f.HasStaged()
			}
			if !staged {
				return m.notify("nothing is staged (space stages the selected file, a stages all)", p.Yellow), true
			}
			g.prompt = &gitPrompt{kind: "commit", title: "Commit · " + v.entry.Name, hint: "enter commit · esc cancel  (amend and the rest: lazygit, L)"}
			return nil, true
		case "y", "Y":
			switch {
			case v.file != nil:
				return m.gitCopyPath(v, v.file.Path, s == "Y"), true
			case v.dir != nil:
				return m.gitCopyPath(v, v.dir.Path, s == "Y"), true
			}
			return nil, true
		case "v":
			if v.file != nil && v.file.Kind == git.Tracked && v.file.HasStaged() && v.file.HasUnstaged() {
				side := git.Staged
				if v.side == git.Staged {
					side = git.Unstaged
				}
				g.sideChoice = &gitSide{file: v.file.Path, side: side}
			}
			return nil, true
		case "enter":
			switch {
			case v.dir != nil:
				if g.folded[v.dir.Path] {
					delete(g.folded, v.dir.Path)
				} else {
					g.folded[v.dir.Path] = true
				}
			case v.file != nil:
				m.focus = paneGitDiff
			}
			return nil, true
		}
	case paneBranches:
		switch s {
		case "enter":
			if v.branch == nil {
				return nil, true
			}
			if v.branch.Current {
				return m.notify("already on "+v.branch.Name, p.Yellow), true
			}
			name := v.branch.Name
			return m.gitAct("switch to "+name, func() git.OpResult { return git.Checkout(root, name) }, "on "+name), true
		case "n":
			g.prompt = &gitPrompt{kind: "branch", title: "New branch · " + v.entry.Name, hint: "enter create and switch · esc cancel"}
			return nil, true
		}
	case paneCommits, paneStash:
		if s == "enter" {
			m.focus = paneGitDiff
			return nil, true
		}
	}
	return nil, false
}

func (m *Model) gitTop() int {
	g := m.gs()
	return min(g.scroll, max(0, len(g.rows.rows)-g.pageRows))
}

func (m *Model) gitStepFile(v gitView, delta int) {
	g := m.gs()
	for i := g.at(paneChanges, len(v.tree)) + delta; i >= 0 && i < len(v.tree); i += delta {
		if !v.tree[i].Dir {
			g.sel[paneChanges] = i
			return
		}
	}
}

func (m *Model) gitCopyPath(v gitView, path string, absolute bool) tea.Cmd {
	if absolute {
		path = filepath.Join(v.entry.Repo.Root, path)
	}
	return m.gitCopy(path)
}

func (m *Model) gitToggleFile(f git.FileChange, root string, hasCommits bool) tea.Cmd {
	if f.HasUnstaged() {
		return m.gitAct("stage "+f.Path, func() git.OpResult { return git.Stage(root, []string{f.Path}) }, "staged "+f.Path)
	}
	return m.gitAct("unstage "+f.Path, func() git.OpResult { return git.Unstage(root, []string{f.Path}, hasCommits) }, "unstaged "+f.Path)
}

// gitToggleDir is space on a folder: stage everything under it, or unstage it when it is all staged.
func (m *Model) gitToggleDir(d git.TreeRow, root string, hasCommits bool) tea.Cmd {
	paths := make([]string, len(d.Files))
	any := false
	for i, f := range d.Files {
		paths[i] = f.Path
		any = any || f.HasUnstaged()
	}
	if any {
		return m.gitAct("stage "+d.Path+"/", func() git.OpResult { return git.Stage(root, paths) }, "staged "+d.Path+"/")
	}
	return m.gitAct("unstage "+d.Path+"/", func() git.OpResult { return git.Unstage(root, paths, hasCommits) }, "unstaged "+d.Path+"/")
}

func (m *Model) gitOpenMulti(kind string) {
	on := m.gitTargets()
	title := fmt.Sprintf("New branch in %d repo%s", len(on), plural(len(on)))
	if kind == "multiSwitch" {
		title = fmt.Sprintf("Switch %d repo%s to branch", len(on), plural(len(on)))
	}
	m.gs().prompt = &gitPrompt{kind: kind, title: title, hint: "enter continue · esc cancel"}
}

func (m *Model) gitSubmit() tea.Cmd {
	g := m.gs()
	p := g.prompt
	g.prompt = nil
	value := strings.TrimSpace(p.in.value())
	entry, ok := g.current()
	if !ok {
		return nil
	}
	if value == "" {
		if p.kind == "commit" {
			return m.notify("empty commit message", theme.Active().Yellow)
		}
		return nil
	}
	root := entry.Repo.Root
	switch p.kind {
	case "commit":
		return m.gitAct("commit", func() git.OpResult { return git.Commit(root, value) }, "committed: "+value)
	case "branch":
		return m.gitNewBranch(value, []git.Entry{entry}, false)
	case "multiBranch":
		return m.gitNewBranch(value, m.gitTargets(), true)
	}
	return m.gitSwitchBranch(value, m.gitTargets())
}
