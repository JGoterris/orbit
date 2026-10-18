// Package state is what orbit remembers on disk about a project: the per-project state directory, the services it
// launched (so a later session can pick them up) and the lock that says which orbit has the project open.
package state

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/platform"
)

// SavedService is what orbit remembers about a service it launched.
type SavedService struct {
	// Pid is the process services' group leader
	Pid int `json:"pid,omitempty"`
	// StartTime is the start time of Pid (guards against pid reuse)
	StartTime int64 `json:"startTime,omitempty"`
	StartedAt int64 `json:"startedAt"`
}

type Saved struct {
	Services map[string]SavedService `json:"services"`
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// Dir is the per-project directory for logs, pids and the lock: <state base>/orbit/<name>-<hash of root>
// (~/.local/state, %LOCALAPPDATA% on Windows).
func Dir(cfg *config.OrbitConfig) string { return DirOf(cfg.Root, cfg.Name) }

// DirOf is Dir for a project known only by its root and name, as it is before its configuration is loaded.
func DirOf(root, name string) string {
	sum := sha1.Sum([]byte(root))
	dir := unsafeName.ReplaceAllString(name+"-"+hex.EncodeToString(sum[:])[:8], "-")
	return filepath.Join(platform.StateBase(), "orbit", dir)
}

// ReadProfiles is the selection of profiles saved for the project in dir; ok is false when none was ever saved
// (a saved empty selection means "no profiles").
func ReadProfiles(dir string) (names []string, ok bool) {
	data, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		return nil, false
	}
	if json.Unmarshal(data, &names) != nil {
		return nil, false
	}
	return names, true
}

// WriteProfiles saves the selection of profiles of the project in dir. Like Write, it is best effort.
func WriteProfiles(dir string, names []string) {
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	if names == nil {
		names = []string{}
	}
	data, err := json.Marshal(names)
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, "profiles.json."+strconv.Itoa(os.Getpid())+".tmp")
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	if os.Rename(tmp, filepath.Join(dir, "profiles.json")) != nil {
		os.Remove(tmp)
	}
}

// SavedProfiles is the lookup LoadOptions.SavedProfiles wants.
func SavedProfiles(root, name string) ([]string, bool) { return ReadProfiles(DirOf(root, name)) }

// ProcFiles are the files a process service writes: its output, its errors and its exit code.
type ProcFiles struct {
	Out  string
	Err  string
	Exit string
}

func Files(dir, service string) ProcFiles {
	base := filepath.Join(dir, "logs", unsafeName.ReplaceAllString(service, "-"))
	return ProcFiles{Out: base + ".out.log", Err: base + ".err.log", Exit: base + ".exit"}
}

// Read returns the saved state of a project; an empty one when there is none or it is unreadable.
func Read(dir string) Saved {
	s := Saved{Services: map[string]SavedService{}}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return s
	}
	var raw Saved
	if json.Unmarshal(data, &raw) != nil || raw.Services == nil {
		return s
	}
	return raw
}

// Write saves the state atomically. It is best effort: orbit keeps working without it.
func Write(dir string, s Saved) {
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, "state.json."+strconv.Itoa(os.Getpid())+".tmp")
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	if os.Rename(tmp, filepath.Join(dir, "state.json")) != nil {
		os.Remove(tmp)
	}
}

// ReadLock is the pid of the live orbit that holds the project, or 0.
func ReadLock(dir string) int {
	if pid := lockPid(dir); platform.PidAlive(pid) {
		return pid
	}
	return 0
}

var (
	locksMu sync.Mutex
	// locks are the orbit.lock files this process holds, by state directory
	locks = map[string]*os.File{}
)

// UnknownHolder is what AcquireLock returns when the lock is held but its holder has not written its pid yet.
const UnknownHolder = -1

// AcquireLock takes the per-project lock. It returns the pid of the orbit that already holds it, or 0 when we got it.
//
// The lock is an OS file lock on orbit.lock (so two orbits starting at once cannot both get it, and it goes away
// with its holder); the file also carries the holder's pid so others can name it. It is best effort: when the
// state directory cannot be written or locked, orbit runs without it.
func AcquireLock(dir string) int {
	locksMu.Lock()
	defer locksMu.Unlock()
	if locks[dir] != nil {
		return 0
	}
	if os.MkdirAll(dir, 0o755) != nil {
		return 0
	}
	f, err := os.OpenFile(filepath.Join(dir, "orbit.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return 0
	}
	for attempt := 0; ; attempt++ {
		ok, err := platform.TryLockFile(f)
		if err != nil {
			f.Close()
			return 0
		}
		if ok {
			break
		}
		// held: the holder writes its pid right after locking
		if pid := lockPid(dir); pid > 0 && pid != os.Getpid() {
			f.Close()
			return pid
		}
		if attempt == 20 {
			f.Close()
			return UnknownHolder
		}
		time.Sleep(50 * time.Millisecond)
	}
	// an orbit that only writes its pid (an older build) still holds the project while it lives
	if holder := ReadLock(dir); holder != 0 && holder != os.Getpid() {
		platform.UnlockFile(f)
		f.Close()
		return holder
	}
	if f.Truncate(0) == nil {
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	}
	locks[dir] = f
	return 0
}

// ReleaseLock gives the lock back, if it is ours.
func ReleaseLock(dir string) {
	locksMu.Lock()
	defer locksMu.Unlock()
	f := locks[dir]
	if f == nil {
		return
	}
	delete(locks, dir)
	// emptied, not removed: an orbit waiting on the old file would lock one nobody else can find any more
	f.Truncate(0)
	platform.UnlockFile(f)
	f.Close()
}

// lockPid is the pid written in orbit.lock, alive or not, or 0.
func lockPid(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, "orbit.lock"))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}
