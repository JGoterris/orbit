package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/platform"
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
	if f := Files("/d", "my-svc"); f.Out != filepath.Join("/d", "logs", "my-svc.out.log") || f.Exit != filepath.Join("/d", "logs", "my-svc.exit") {
		t.Fatalf("files = %+v", f)
	}
	f := Files("/d", "my svc")
	if !strings.HasPrefix(filepath.Base(f.Out), "my-svc-") || f.Out == Files("/d", "my-svc").Out || f.Out == Files("/d", "my/svc").Out {
		t.Fatalf("files = %+v", f)
	}
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "orbit.lock")
	if holder := AcquireLock(dir); holder != 0 {
		t.Fatalf("first acquire = %d", holder)
	}
	if ReadLock(dir) != os.Getpid() {
		t.Fatal("lock should name us")
	}
	if holder := AcquireLock(dir); holder != 0 {
		t.Fatalf("acquiring again from the holder = %d", holder)
	}
	// the OS lock keeps anyone else out, whatever the file says
	other, err := os.OpenFile(file, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := platform.TryLockFile(other); ok {
		t.Fatal("another open file got the lock")
	}
	other.Close()
	ReleaseLock(dir)
	if ReadLock(dir) != 0 {
		t.Fatal("released lock should name nobody")
	}

	// a lock file written by a live process that is not us is respected (pid 1 is always there on POSIX)
	if !platform.IsWindows {
		os.WriteFile(file, []byte("1"), 0o644)
		if holder := AcquireLock(dir); holder != 1 {
			t.Fatalf("held by pid 1, acquire = %d", holder)
		}
	}
	// a stale pid is taken over
	os.WriteFile(file, []byte("2147483646"), 0o644)
	if holder := AcquireLock(dir); holder != 0 {
		t.Fatalf("stale lock, acquire = %d", holder)
	}
	ReleaseLock(dir)
}

func TestProfilesSelectionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ReadProfiles(dir); ok {
		t.Fatal("nothing saved yet")
	}
	WriteProfiles(dir, []string{"debug", "light"})
	if got, ok := ReadProfiles(dir); !ok || len(got) != 2 || got[0] != "debug" || got[1] != "light" {
		t.Fatalf("read = %v, %v", got, ok)
	}
	// saving "none" is a selection too: it is not the same as never having saved
	WriteProfiles(dir, nil)
	if got, ok := ReadProfiles(dir); !ok || len(got) != 0 {
		t.Fatalf("read = %v, %v", got, ok)
	}
}
