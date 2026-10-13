// Package watch restarts-on-change: it watches a service's files and batches the changes.
package watch

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/fsnotify/fsnotify"

	"github.com/JGoterris/orbit/internal/config"
)

// DefaultIgnore: never worth restarting for: VCS internals, dependencies and editor scratch files.
var DefaultIgnore = []string{"**/.git/**", "**/node_modules/**", "**/*.swp", "**/*.swx", "**/*~", "**/.#*", "**/4913"}

// Root is a directory to register watchers on.
type Root struct {
	Dir       string
	Recursive bool
}

func hasGlob(s string) bool { return strings.ContainsAny(s, "*?[]{}!") }

// Roots are the directories to watch: the static prefix of each pattern, so `src/**` watches `src/` instead of
// the whole cwd. A pattern with no directory part (`package.json`) watches the cwd flat.
func Roots(patterns []string) []Root {
	var order []string
	recursive := map[string]bool{}
	for _, pattern := range patterns {
		segments := strings.Split(strings.TrimPrefix(pattern, "./"), "/")
		n := 0
		for n < len(segments)-1 && !hasGlob(segments[n]) {
			n++
		}
		// anything left beyond a single path segment (or a trailing `**`) may live in a subdirectory
		rec := len(segments)-n > 1 || strings.Contains(segments[len(segments)-1], "**")
		dir := strings.Join(segments[:n], "/")
		if _, seen := recursive[dir]; !seen {
			order = append(order, dir)
		}
		recursive[dir] = recursive[dir] || rec
	}
	var roots []Root
	for _, dir := range order {
		roots = append(roots, Root{dir, recursive[dir]})
	}
	// a recursive root already covers the roots below it
	var out []Root
	for _, r := range roots {
		covered := false
		for _, o := range roots {
			if o != r && o.Recursive && (o.Dir == "" || strings.HasPrefix(r.Dir, o.Dir+"/")) {
				covered = true
			}
		}
		if !covered {
			out = append(out, r)
		}
	}
	return out
}

// Hooks are what a FileWatcher tells its owner.
type Hooks struct {
	// OnTrigger: debounce and cooldown elapsed; these files changed since the last trigger
	OnTrigger func(files []string)
	OnError   func(message string)
	// OnPending: changes are queued (waiting for the debounce / cooldown), or the queue emptied
	OnPending func(pending bool)
}

// FileWatcher batches changes: it fires once the files have been quiet for `debounce`, and never twice within
// `cooldown`. Changes arriving meanwhile join the same batch.
type FileWatcher struct {
	cwd   string
	cfg   config.WatchConfig
	hooks Hooks

	mu       sync.Mutex
	batch    []string
	inBatch  map[string]bool
	timer    *time.Timer
	lastFire time.Time
	paused   bool
	closed   bool

	watcher *fsnotify.Watcher
	// recursive roots, as absolute paths: new directories below them get a watch too
	recursiveRoots []string
	stop           chan struct{}
}

func New(cwd string, cfg config.WatchConfig, hooks Hooks) *FileWatcher {
	return &FileWatcher{cwd: cwd, cfg: cfg, hooks: hooks, inBatch: map[string]bool{}}
}

func (w *FileWatcher) Paused() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.paused
}

func (w *FileWatcher) Pending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.batch) > 0
}

// SetPaused stops (or resumes) reacting to changes; pausing drops what was queued.
func (w *FileWatcher) SetPaused(v bool) {
	w.mu.Lock()
	if v == w.paused {
		w.mu.Unlock()
		return
	}
	w.paused = v
	var notify bool
	if v {
		notify = w.clearLocked()
	}
	w.mu.Unlock()
	if notify && w.hooks.OnPending != nil {
		w.hooks.OnPending(false)
	}
}

func (w *FileWatcher) ignored(rel string) bool {
	for _, g := range DefaultIgnore {
		if ok, _ := doublestar.Match(g, rel); ok {
			return true
		}
	}
	for _, g := range w.cfg.Ignore {
		if ok, _ := doublestar.Match(g, rel); ok {
			return true
		}
	}
	return false
}

func (w *FileWatcher) matches(rel string) bool {
	for _, p := range w.cfg.Paths {
		if ok, _ := doublestar.Match(strings.TrimPrefix(p, "./"), rel); ok {
			return true
		}
	}
	return false
}

// Start registers the OS watchers.
func (w *FileWatcher) Start() {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		w.fail(err)
		return
	}
	w.watcher = fw
	w.stop = make(chan struct{})
	for _, root := range Roots(w.cfg.Paths) {
		abs := w.cwd
		if root.Dir != "" {
			abs = filepath.Join(w.cwd, filepath.FromSlash(root.Dir))
		}
		if _, err := os.Stat(abs); err != nil {
			dir := root.Dir
			if dir == "" {
				dir = "."
			}
			w.hooks.OnError("watch: " + dir + " does not exist, not watching it")
			continue
		}
		if root.Recursive {
			w.recursiveRoots = append(w.recursiveRoots, abs)
			w.addTree(abs)
		} else if err := fw.Add(abs); err != nil {
			w.fail(err)
		}
	}
	go w.loop()
}

// addTree watches dir and every directory below it that is not ignored.
func (w *FileWatcher) addTree(dir string) {
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != dir {
			rel, rerr := filepath.Rel(w.cwd, path)
			if rerr == nil && w.ignored(filepath.ToSlash(rel)+"/x") {
				return filepath.SkipDir
			}
		}
		if err := w.watcher.Add(path); err != nil {
			w.fail(err)
		}
		return nil
	})
}

func (w *FileWatcher) loop() {
	for {
		select {
		case <-w.stop:
			return
		case ev, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Create) {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() && w.underRecursiveRoot(ev.Name) {
					w.addTree(ev.Name)
				}
			}
			if rel, err := filepath.Rel(w.cwd, ev.Name); err == nil {
				w.Touch(filepath.ToSlash(rel))
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.fail(err)
		}
	}
}

func (w *FileWatcher) underRecursiveRoot(path string) bool {
	for _, r := range w.recursiveRoots {
		if path == r || strings.HasPrefix(path, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// Close stops watching and drops what was queued.
func (w *FileWatcher) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	notify := w.clearLocked()
	w.mu.Unlock()
	if notify && w.hooks.OnPending != nil {
		w.hooks.OnPending(false)
	}
	if w.stop != nil {
		close(w.stop)
	}
	if w.watcher != nil {
		w.watcher.Close()
	}
}

// Touch registers a changed file (relative to cwd, with forward slashes) if it matches `paths` and not `ignore`.
// Exposed for tests.
func (w *FileWatcher) Touch(file string) {
	if !w.matches(file) || w.ignored(file) {
		return
	}
	// macOS and Windows also report the folders that contain a changed file: only files are changes
	if st, err := os.Stat(filepath.Join(w.cwd, filepath.FromSlash(file))); err == nil && st.IsDir() {
		return
	}
	w.mu.Lock()
	if w.paused || w.closed {
		w.mu.Unlock()
		return
	}
	wasEmpty := len(w.batch) == 0
	if !w.inBatch[file] {
		w.inBatch[file] = true
		w.batch = append(w.batch, file)
	}
	w.scheduleLocked(time.Duration(w.cfg.Debounce) * time.Millisecond)
	w.mu.Unlock()
	if wasEmpty && w.hooks.OnPending != nil {
		w.hooks.OnPending(true)
	}
}

func (w *FileWatcher) scheduleLocked(delay time.Duration) {
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(delay, w.fire)
}

func (w *FileWatcher) fire() {
	w.mu.Lock()
	if w.closed || w.paused {
		w.mu.Unlock()
		return
	}
	cooldown := time.Duration(w.cfg.Cooldown) * time.Millisecond
	if since := time.Since(w.lastFire); !w.lastFire.IsZero() && since < cooldown {
		w.scheduleLocked(cooldown - since)
		w.mu.Unlock()
		return
	}
	files := w.batch
	w.batch, w.inBatch = nil, map[string]bool{}
	w.lastFire = time.Now()
	w.mu.Unlock()
	if w.hooks.OnPending != nil {
		w.hooks.OnPending(false)
	}
	if len(files) > 0 && w.hooks.OnTrigger != nil {
		w.hooks.OnTrigger(files)
	}
}

// clearLocked drops the queue and reports whether there was one.
func (w *FileWatcher) clearLocked() bool {
	if w.timer != nil {
		w.timer.Stop()
	}
	had := len(w.batch) > 0
	w.batch, w.inBatch = nil, map[string]bool{}
	return had
}

func (w *FileWatcher) fail(err error) {
	msg := "watch: " + err.Error()
	if strings.Contains(err.Error(), "too many open files") || strings.Contains(err.Error(), "no space left") {
		msg += " (raise fs.inotify.max_user_watches)"
	}
	if w.hooks.OnError != nil {
		w.hooks.OnError(msg)
	}
}
