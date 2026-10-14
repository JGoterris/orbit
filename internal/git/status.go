// Package git reads and drives git repositories through the git command line: status, diffs, branches, commits,
// stashes and the few operations orbit offers (stage, commit, switch, fetch, pull, push).
package git

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JGoterris/orbit/internal/proc"
)

// FindRoot is the nearest ancestor of dir (inclusive) holding a `.git` entry (a directory, or a file for
// worktrees and submodules); "" when there is none.
func FindRoot(dir string) string {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// Kind of an entry of `git status`.
type Kind string

const (
	Tracked   Kind = "tracked"
	Untracked Kind = "untracked"
	Conflict  Kind = "conflict"
)

// FileChange is one path in `git status`. X is the index column, Y the worktree column ('.' = unchanged).
type FileChange struct {
	Path string
	// Orig is the path before a rename or copy
	Orig string
	X, Y byte
	Kind Kind
}

// Status is the state of a repository's working tree and branch.
type Status struct {
	// Branch is the current branch, "" when HEAD is detached
	Branch string
	// Oid is the short commit id when detached
	Oid      string
	Upstream string
	Ahead    int
	Behind   int
	// HasCommits is false in a repo without commits yet
	HasCommits bool
	Files      []FileChange
}

func (f FileChange) HasStaged() bool { return f.Kind == Tracked && f.X != '.' }
func (f FileChange) HasUnstaged() bool {
	return f.Kind == Untracked || f.Kind == Conflict || f.Y != '.'
}

// ParseStatus parses `git status --porcelain=v2 --branch -z` output.
func ParseStatus(out string) Status {
	st := Status{HasCommits: true}
	recs := strings.Split(out, "\x00")
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if r == "" {
			continue
		}
		switch {
		case strings.HasPrefix(r, "# "):
			key, value, _ := strings.Cut(r[2:], " ")
			switch key {
			case "branch.head":
				if value != "(detached)" {
					st.Branch = value
				}
			case "branch.oid":
				st.HasCommits = value != "(initial)"
				if st.HasCommits {
					st.Oid = value[:min(7, len(value))]
				}
			case "branch.upstream":
				st.Upstream = value
			case "branch.ab":
				var a, b int
				if f := strings.Fields(value); len(f) == 2 {
					a, _ = strconv.Atoi(strings.TrimPrefix(f[0], "+"))
					b, _ = strconv.Atoi(strings.TrimPrefix(f[1], "-"))
					st.Ahead, st.Behind = a, b
				}
			}
		case r[0] == '?':
			st.Files = append(st.Files, FileChange{Path: r[2:], X: '.', Y: '?', Kind: Untracked})
		case r[0] == '1':
			f := strings.SplitN(r, " ", 9)
			if len(f) == 9 && len(f[1]) == 2 {
				st.Files = append(st.Files, FileChange{Path: f[8], X: f[1][0], Y: f[1][1], Kind: Tracked})
			}
		case r[0] == '2':
			f := strings.SplitN(r, " ", 10)
			if len(f) == 10 && len(f[1]) == 2 {
				fc := FileChange{Path: f[9], X: f[1][0], Y: f[1][1], Kind: Tracked}
				if i+1 < len(recs) {
					i++
					fc.Orig = recs[i]
				}
				st.Files = append(st.Files, fc)
			}
		case r[0] == 'u':
			f := strings.SplitN(r, " ", 11)
			if len(f) == 11 && len(f[1]) == 2 {
				st.Files = append(st.Files, FileChange{Path: f[10], X: f[1][0], Y: f[1][1], Kind: Conflict})
			}
		}
	}
	return st
}

// Env is the environment of every git call: no prompts, no optional index locks (orbit polls), stable English
// output.
func Env() []string {
	return proc.EnvList(map[string]string{"GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0", "LC_ALL": "C"})
}

func run(root string, args []string, opts proc.Options) proc.Result {
	opts.Dir, opts.Env = root, Env()
	return proc.Exec(append([]string{"git"}, args...), opts)
}

// ReadStatus reads the status of the repository at root; ok is false when git could not.
func ReadStatus(root string) (Status, bool) {
	res := run(root, []string{"status", "--porcelain=v2", "--branch", "--untracked-files=all", "-z"}, proc.Options{})
	if res.Code != 0 {
		return Status{}, false
	}
	return ParseStatus(res.Stdout), true
}
