package git

import (
	"strings"
	"sync"
	"time"

	"github.com/JGoterris/orbit/internal/proc"
)

// OpResult is the outcome of a git operation.
type OpResult struct {
	OK bool
	// Message is git's own words: the first useful line of stderr, else the last of stdout
	Message string
}

func firstUseful(r proc.Result) string {
	useful := func(s string) []string {
		var out []string
		for _, l := range strings.Split(s, "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !strings.HasPrefix(l, "hint:") {
				out = append(out, l)
			}
		}
		return out
	}
	if e := useful(r.Stderr); len(e) > 0 {
		return e[0]
	}
	if o := useful(r.Stdout); len(o) > 0 {
		return o[len(o)-1]
	}
	return ""
}

// CmdEntry is one git command orbit ran on the user's behalf (not the polling reads: status, log, branches, diffs).
type CmdEntry struct {
	// Root is the repo it ran in
	Root string
	// Args are the arguments after `git`
	Args    []string
	OK      bool
	Message string
	At      time.Time
	Ms      int64
}

const maxCmdLog = 300

// CommandLog is what the Git view's Commands panel shows; it is filled by the operations below.
type CommandLog struct {
	mu      sync.Mutex
	entries []CmdEntry
	version int
}

// Commands is the process-wide command log.
var Commands = &CommandLog{}

func (c *CommandLog) add(e CmdEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
	if len(c.entries) > maxCmdLog {
		c.entries = c.entries[len(c.entries)-maxCmdLog:]
	}
	c.version++
}

// Entries are the logged commands, oldest first.
func (c *CommandLog) Entries() []CmdEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]CmdEntry(nil), c.entries...)
}

func (c *CommandLog) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.version++
}

// Version changes whenever the log does.
func (c *CommandLog) Version() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

// FormatArgs is `git commit -F -` as the user would read it: arguments with spaces are quoted.
func FormatArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\n\"'") {
			out[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, " ")
}

// do runs one operation; every one goes through here, so each lands in the command log.
func do(root string, args []string, opts proc.Options) OpResult {
	at := time.Now()
	r := run(root, args, opts)
	res := OpResult{OK: r.Code == 0, Message: firstUseful(r)}
	Commands.add(CmdEntry{Root: root, Args: args, OK: res.OK, Message: res.Message, At: at, Ms: time.Since(at).Milliseconds()})
	return res
}

// ---------------------------------------------------------------- staging

func Stage(root string, paths []string) OpResult {
	return do(root, append([]string{"add", "--"}, paths...), proc.Options{})
}

func StageAll(root string) OpResult { return do(root, []string{"add", "-A"}, proc.Options{}) }

// Unstage takes paths out of the index. A repo without commits has no HEAD to restore from.
func Unstage(root string, paths []string, hasCommits bool) OpResult {
	if hasCommits {
		return do(root, append([]string{"restore", "--staged", "--"}, paths...), proc.Options{})
	}
	return do(root, append([]string{"rm", "--cached", "-r", "-q", "--"}, paths...), proc.Options{})
}

func UnstageAll(root string, hasCommits bool) OpResult {
	if hasCommits {
		return do(root, []string{"reset", "-q"}, proc.Options{})
	}
	return do(root, []string{"rm", "--cached", "-r", "-q", "."}, proc.Options{})
}

// ---------------------------------------------------------------- commits

func Commit(root, message string) OpResult {
	in := message + "\n"
	return do(root, []string{"commit", "-F", "-"}, proc.Options{Input: &in})
}

// CommitInfo is one line of the graph.
type CommitInfo struct {
	// Graph is the `--graph` drawing in front of the commit (empty when there is none)
	Graph   string
	Hash    string
	Author  string
	When    string
	Refs    string
	Subject string
}

const sep = "\x1f"

// ParseLog parses `git log --graph --format=\x1f%h…` lines; pure connector lines are skipped.
func ParseLog(out string) []CommitInfo {
	var commits []CommitInfo
	for _, line := range strings.Split(out, "\n") {
		at := strings.Index(line, sep)
		if at == -1 {
			continue
		}
		f := strings.Split(line[at+1:], sep)
		if len(f) < 5 {
			continue
		}
		commits = append(commits, CommitInfo{
			Graph: line[:at], Hash: f[0], Author: f[1], When: f[2], Refs: f[3], Subject: strings.Join(f[4:], sep),
		})
	}
	return commits
}

func Log(root string, limit int) []CommitInfo {
	if limit <= 0 {
		limit = 200
	}
	r := run(root, []string{"log", "--graph", "--decorate=short", "-n" + itoa(limit),
		"--format=" + sep + "%h" + sep + "%an" + sep + "%ar" + sep + "%D" + sep + "%s"}, proc.Options{})
	if r.Code != 0 {
		return nil
	}
	return ParseLog(r.Stdout)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------- branches

// BranchInfo is one local branch.
type BranchInfo struct {
	Name     string
	Current  bool
	Upstream string
	// Track is "ahead 1", "behind 2", "gone"… as git prints it
	Track string
	// When is the relative date of the last commit
	When string
}

func ParseBranches(out string) []BranchInfo {
	var list []BranchInfo
	for _, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		f := strings.Split(l, sep)
		for len(f) < 5 {
			f = append(f, "")
		}
		track := strings.NewReplacer("[", "", "]", "").Replace(f[3])
		list = append(list, BranchInfo{Name: f[1], Current: f[0] == "*", Upstream: f[2], Track: track, When: f[4]})
	}
	return list
}

func Branches(root string) []BranchInfo {
	r := run(root, []string{"for-each-ref", "--sort=-committerdate",
		"--format=%(HEAD)" + sep + "%(refname:short)" + sep + "%(upstream:short)" + sep + "%(upstream:track)" + sep + "%(committerdate:relative)",
		"refs/heads"}, proc.Options{})
	if r.Code != 0 {
		return nil
	}
	return ParseBranches(r.Stdout)
}

func Checkout(root, branch string) OpResult {
	return do(root, []string{"switch", branch}, proc.Options{})
}
func CreateBranch(root, name string) OpResult {
	return do(root, []string{"switch", "-c", name}, proc.Options{})
}

// ---------------------------------------------------------------- remotes

func Fetch(root string) OpResult {
	return do(root, []string{"fetch", "--prune"}, proc.Options{Detached: true})
}

func Pull(root string) OpResult {
	return do(root, []string{"pull", "--ff-only"}, proc.Options{Detached: true})
}

func Push(root, branch string, hasUpstream bool) OpResult {
	args := []string{"push"}
	if !hasUpstream {
		args = []string{"push", "-u", "origin", branch}
	}
	return do(root, args, proc.Options{Detached: true})
}

// ---------------------------------------------------------------- stash

// StashInfo is one stash entry.
type StashInfo struct {
	Ref string
	// Hash is the stash commit: stable, unlike `stash@{n}`, which shifts when a new stash is made
	Hash    string
	Message string
}

func ParseStashes(out string) []StashInfo {
	var list []StashInfo
	for _, l := range strings.Split(out, "\n") {
		if l == "" {
			continue
		}
		f := strings.Split(l, sep)
		if len(f) < 2 {
			continue
		}
		list = append(list, StashInfo{Ref: f[0], Hash: f[1], Message: strings.Join(f[2:], sep)})
	}
	return list
}

func Stashes(root string) []StashInfo {
	r := run(root, []string{"stash", "list", "--format=%gd" + sep + "%H" + sep + "%s"}, proc.Options{})
	if r.Code != 0 {
		return nil
	}
	return ParseStashes(r.Stdout)
}
