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
func Dir(cfg *config.OrbitConfig) string {
	sum := sha1.Sum([]byte(cfg.Root))
	name := unsafeName.ReplaceAllString(cfg.Name+"-"+hex.EncodeToString(sum[:])[:8], "-")
	return filepath.Join(platform.StateBase(), "orbit", name)
}

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
	data, err := os.ReadFile(filepath.Join(dir, "orbit.lock"))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || !platform.PidAlive(pid) {
		return 0
	}
	return pid
}

// AcquireLock takes the per-project lock. It returns the pid of the orbit that already holds it, or 0 when we got it.
func AcquireLock(dir string) int {
	if holder := ReadLock(dir); holder != 0 && holder != os.Getpid() {
		return holder
	}
	if os.MkdirAll(dir, 0o755) == nil {
		os.WriteFile(filepath.Join(dir, "orbit.lock"), []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	return 0
}

// ReleaseLock gives the lock back, if it is ours.
func ReleaseLock(dir string) {
	if ReadLock(dir) == os.Getpid() {
		os.Remove(filepath.Join(dir, "orbit.lock"))
	}
}
