package git

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/JGoterris/orbit/internal/proc"
)

// Hunk is one `@@` block of a diff.
type Hunk struct {
	// Header is the `@@ -a,b +c,d @@ context` line
	Header string
	// Lines are every line after the header, including " ", "+", "-" and "\ No newline" markers
	Lines    []string
	OldStart int
	NewStart int
}

// DiffFile is one file of a unified diff.
type DiffFile struct {
	// Header is `diff --git`, `index`, `---`, `+++`, mode / rename lines: everything before the first hunk
	Header []string
	Hunks  []Hunk
	Binary bool
	// Path is the path shown for the file (the new one, or the old one for deletions)
	Path string
}

var diffGitRe = regexp.MustCompile(`^diff --git a/(.*) b/(.*)$`)
var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)`)

func filePath(header []string) string {
	var plus, minus string
	for _, l := range header {
		if strings.HasPrefix(l, "+++ ") && plus == "" {
			plus = l[4:]
		}
		if strings.HasPrefix(l, "--- ") && minus == "" {
			minus = l[4:]
		}
	}
	pick := plus
	if pick == "" || pick == "/dev/null" {
		pick = minus
	}
	if pick != "" && pick != "/dev/null" {
		if strings.HasPrefix(pick, "a/") || strings.HasPrefix(pick, "b/") {
			pick = pick[2:]
		}
		return pick
	}
	if len(header) > 0 {
		if m := diffGitRe.FindStringSubmatch(header[0]); m != nil {
			return m[2]
		}
		return header[0]
	}
	return ""
}

// ParseDiff parses unified diff text (`git diff`, `git show`) into files and hunks.
func ParseDiff(text string) []DiffFile {
	var files []DiffFile
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			files = append(files, DiffFile{Header: []string{line}})
		case len(files) == 0:
			continue
		case strings.HasPrefix(line, "@@"):
			f := &files[len(files)-1]
			h := Hunk{Header: line}
			if m := hunkRe.FindStringSubmatch(line); m != nil {
				h.OldStart, _ = strconv.Atoi(m[1])
				h.NewStart, _ = strconv.Atoi(m[2])
			}
			f.Hunks = append(f.Hunks, h)
		default:
			f := &files[len(files)-1]
			if n := len(f.Hunks); n > 0 {
				f.Hunks[n-1].Lines = append(f.Hunks[n-1].Lines, line)
				continue
			}
			if strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch") {
				f.Binary = true
			}
			f.Header = append(f.Header, line)
		}
	}
	for i := range files {
		files[i].Path = filePath(files[i].Header)
	}
	return files
}

// DiffSide says which diff of a change to show.
type DiffSide string

const (
	Unstaged DiffSide = "unstaged"
	Staged   DiffSide = "staged"
)

var diffBase = []string{"diff", "--no-color", "--no-ext-diff"}

// FileDiff is the diff of one change: the worktree against the index, or the index against HEAD.
func FileDiff(root string, f FileChange, side DiffSide) string {
	if f.Kind == Untracked {
		// exit code 1 just means "there are differences"
		return run(root, append(append([]string{}, diffBase...), "--no-index", "--", "/dev/null", f.Path), proc.Options{}).Stdout
	}
	paths := []string{f.Path}
	if f.Orig != "" && side == Staged {
		paths = []string{f.Orig, f.Path}
	}
	args := append([]string{}, diffBase...)
	if side == Staged {
		args = append(args, "--cached")
	}
	args = append(append(args, "--"), paths...)
	return run(root, args, proc.Options{}).Stdout
}

// CommitDiff is what a commit changed.
func CommitDiff(root, rev string) string {
	return run(root, []string{"show", "--format=", "--no-color", "--no-ext-diff", rev}, proc.Options{}).Stdout
}

// StashDiff is what a stash holds.
func StashDiff(root, ref string) string {
	return run(root, []string{"stash", "show", "-p", "--no-color", ref}, proc.Options{}).Stdout
}

// RangeDiff is the diff between two revisions (`a..b`, a branch name…).
func RangeDiff(root, rng string) string {
	return run(root, append(append([]string{}, diffBase...), rng), proc.Options{}).Stdout
}
