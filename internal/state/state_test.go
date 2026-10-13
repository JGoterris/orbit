package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JGoterris/orbit/internal/config"
)

func TestDirIsPerProject(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/s")
	a := Dir(&config.OrbitConfig{Name: "demo", Root: "/work/a"})
	b := Dir(&config.OrbitConfig{Name: "demo", Root: "/work/b"})
	if a == b || !strings.HasPrefix(a, filepath.Join("/s", "orbit", "demo-")) {
		t.Fatalf("a = %s, b = %s", a, b)
	}
	if got := Dir(&config.OrbitConfig{Name: "we ird/name", Root: "/w"}); strings.ContainsAny(filepath.Base(got), " /") {
		t.Fatalf("unsafe name: %s", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := Read(dir); len(got.Services) != 0 {
		t.Fatalf("empty dir = %+v", got)
	}
	want := Saved{Services: map[string]SavedService{"api": {Pid: 42, StartTime: 7, StartedAt: 99}}}
	Write(dir, want)
	if got := Read(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("read = %+v, want %+v", got, want)
	}
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("{broken"), 0o644)
	if got := Read(dir); len(got.Services) != 0 {
		t.Fatalf("broken file = %+v", got)
	}
}

func TestFilesNames(t *testing.T) {
	f := Files("/d", "my svc")
	if f.Out != filepath.Join("/d", "logs", "my-svc.out.log") || f.Exit != filepath.Join("/d", "logs", "my-svc.exit") {
		t.Fatalf("files = %+v", f)
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	if holder := AcquireLock(dir); holder != 0 {
		t.Fatalf("first acquire = %d", holder)
	}
	if ReadLock(dir) != os.Getpid() {
		t.Fatal("lock should name us")
	}
	// a lock held by a live process that is not us is respected
	os.WriteFile(filepath.Join(dir, "orbit.lock"), []byte("1"), 0o644)
	if holder := AcquireLock(dir); holder != 1 {
		t.Fatalf("held by pid 1, acquire = %d", holder)
	}
	// a stale pid is taken over
	os.WriteFile(filepath.Join(dir, "orbit.lock"), []byte("2147483646"), 0o644)
	if holder := AcquireLock(dir); holder != 0 {
		t.Fatalf("stale lock, acquire = %d", holder)
	}
	ReleaseLock(dir)
	if _, err := os.Stat(filepath.Join(dir, "orbit.lock")); err == nil {
		t.Fatal("lock should be gone")
	}
}
