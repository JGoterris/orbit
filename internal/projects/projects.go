// Package projects is the registry of the projects orbit has opened, kept in ~/.config/orbit/projects.json and
// shared by every project, plus the path helpers the project picker needs.
package projects

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/state"
	"github.com/JGoterris/orbit/internal/userconfig"
)

// Entry is a project orbit has opened before.
type Entry struct {
	// Path is the absolute directory (the folder holding orbit.yaml / docker-compose.yml, or any folder)
	Path string `json:"path"`
	Name string `json:"name"`
	// LastOpened is the epoch ms of the last time it was opened
	LastOpened int64 `json:"lastOpened"`
	Pinned     bool  `json:"pinned,omitempty"`
}

// maxRecent: unpinned entries beyond this many (oldest first) are dropped on write.
const maxRecent = 30

func file() string { return filepath.Join(userconfig.Dir(), "projects.json") }

// Read returns the registry; empty when there is none or it is unreadable. Bad entries are skipped.
func Read() []Entry {
	data, err := os.ReadFile(file())
	if err != nil {
		return nil
	}
	var raw struct {
		Projects []map[string]any `json:"projects"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	var out []Entry
	for _, p := range raw.Projects {
		path, _ := p["path"].(string)
		name, ok := p["name"].(string)
		if path == "" || !ok {
			continue
		}
		e := Entry{Path: path, Name: name}
		if f, ok := p["lastOpened"].(float64); ok {
			e.LastOpened = int64(f)
		}
		e.Pinned = p["pinned"] == true
		out = append(out, e)
	}
	return out
}

func write(list []Entry) bool {
	var pinned, recent []Entry
	for _, p := range list {
		if p.Pinned {
			pinned = append(pinned, p)
		} else {
			recent = append(recent, p)
		}
	}
	recent = Sort(recent)
	if len(recent) > maxRecent {
		recent = recent[:maxRecent]
	}
	return userconfig.WriteJSONAtomic(file(), map[string]any{"projects": append(pinned, recent...)})
}

// Sort puts pinned first, then most recently opened.
func Sort(list []Entry) []Entry {
	out := slices.Clone(list)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].LastOpened > out[j].LastOpened
	})
	return out
}

// Register records that path was just opened (keeping its pin). It reports whether the registry was written.
func Register(path, name string, now int64) bool {
	if now == 0 {
		now = time.Now().UnixMilli()
	}
	list := Read()
	entry := Entry{Path: path, Name: name, LastOpened: now}
	var rest []Entry
	for _, p := range list {
		if p.Path == path {
			entry.Pinned = p.Pinned
		} else {
			rest = append(rest, p)
		}
	}
	return write(append(rest, entry))
}

func SetPinned(path string, pinned bool) bool {
	list := Read()
	for i := range list {
		if list[i].Path == path {
			list[i].Pinned = pinned
		}
	}
	return write(list)
}

func Forget(path string) bool {
	var rest []Entry
	for _, p := range Read() {
		if p.Path != path {
			rest = append(rest, p)
		}
	}
	return write(rest)
}

// Status is what is going on in a remembered project.
type Status struct {
	// Exists: the folder is still there
	Exists bool
	// OpenIn is the pid of another orbit that has the project open right now (0: none)
	OpenIn int
	// Running counts the processes orbit left running there (containers are not tracked)
	Running int
}

// StatusOf looks at the project's folder, its lock and the processes it left behind.
func StatusOf(p Entry) Status {
	dir := state.Dir(&config.OrbitConfig{Name: p.Name, Root: p.Path})
	st := Status{}
	if _, err := os.Stat(p.Path); err == nil {
		st.Exists = true
	}
	if holder := state.ReadLock(dir); holder != 0 && holder != os.Getpid() {
		st.OpenIn = holder
	}
	for _, s := range state.Read(dir).Services {
		if s.Pid == 0 || !platform.PidAlive(s.Pid) {
			continue
		}
		if s.StartTime != 0 {
			if now, ok := platform.ProcStartTime(s.Pid); !ok || now != s.StartTime {
				continue
			}
		}
		st.Running++
	}
	return st
}

// ExpandPath turns `~` and relative paths into an absolute path.
func ExpandPath(input, cwd string) string {
	t := strings.TrimSpace(input)
	home, _ := os.UserHomeDir()
	switch {
	case t == "~":
		return home
	case strings.HasPrefix(t, "~/") || strings.HasPrefix(t, `~\`):
		return filepath.Join(home, t[2:])
	}
	if filepath.IsAbs(t) {
		return filepath.Clean(t)
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Join(cwd, t)
}

var pathLike = regexp.MustCompile(`^(/|\\|[a-zA-Z]:[\\/]|~|\.{1,2}([\\/]|$))`)

// LooksLikePath says whether what was typed in the project picker is a path rather than a search.
func LooksLikePath(input string) bool { return pathLike.MatchString(strings.TrimSpace(input)) }

// CompletePath is shell-style tab completion of a directory path: a single match is completed (with a trailing
// separator), several are completed up to their common prefix, none leaves the input as is.
func CompletePath(input, cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	home, _ := os.UserHomeDir()
	sep := string(filepath.Separator)
	full := ExpandPath(input, cwd)
	endsInSlash := strings.HasSuffix(input, "/") || strings.HasSuffix(input, `\`)
	var parent, prefix string
	if endsInSlash || input == "~" {
		parent, prefix = full, ""
	} else {
		parent, prefix = filepath.Dir(full), filepath.Base(full)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return input
	}
	var names []string
	for _, e := range entries {
		isDir := e.IsDir()
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(parent, e.Name())); err == nil && st.IsDir() {
				isDir = true
			}
		}
		if !isDir || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return input
	}
	sort.Strings(names)
	common := names[0]
	for _, n := range names {
		for !strings.HasPrefix(n, common) {
			common = common[:len(common)-1]
		}
	}
	if len(names) > 1 && common == prefix {
		return input // several candidates, nothing more in common
	}
	done := filepath.Join(parent, common)
	if len(names) == 1 {
		done += sep
	}
	// keep the way the user wrote it (~ stays ~, relative stays relative)
	switch {
	case strings.HasPrefix(input, "~"):
		if strings.HasPrefix(done, home) {
			return "~" + done[len(home):]
		}
	case !filepath.IsAbs(input):
		if strings.HasPrefix(done, cwd+sep) {
			lead := ""
			if strings.HasPrefix(input, "./") || strings.HasPrefix(input, `.\`) {
				lead = "." + sep
			}
			return lead + done[len(cwd)+1:]
		}
	}
	return done
}

// Find resolves what `orbit open <x>` was given: a registered project by exact name, then by path, then by a name
// fragment. It returns an error that says what went wrong when nothing (or more than one project) matches.
func Find(query string, list []Entry) (Entry, error) {
	q := strings.TrimSpace(query)
	var byName []Entry
	for _, p := range list {
		if p.Name == q {
			byName = append(byName, p)
		}
	}
	if len(byName) == 1 {
		return byName[0], nil
	}
	if len(byName) > 1 {
		paths := make([]string, len(byName))
		for i, p := range byName {
			paths[i] = p.Path
		}
		return Entry{}, fmt.Errorf("several projects are called %q: %s (use the path)", q, strings.Join(paths, ", "))
	}
	path := ExpandPath(q, "")
	for _, p := range list {
		if p.Path == path {
			return p, nil
		}
	}
	var fuzzy []Entry
	for _, p := range list {
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(q)) {
			fuzzy = append(fuzzy, p)
		}
	}
	if len(fuzzy) == 1 {
		return fuzzy[0], nil
	}
	if len(fuzzy) > 1 {
		names := make([]string, len(fuzzy))
		for i, p := range fuzzy {
			names[i] = p.Name
		}
		return Entry{}, fmt.Errorf("%q matches %s", q, strings.Join(names, ", "))
	}
	return Entry{}, fmt.Errorf("no project %q (see `orbit projects`)", q)
}
