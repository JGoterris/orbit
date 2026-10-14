package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/config"
)

func cfg(edit ...func(*config.WatchConfig)) config.WatchConfig {
	c := config.WatchConfig{Paths: []string{"src/**", "package.json"}, Ignore: []string{}, Debounce: 50, Cooldown: 300}
	for _, e := range edit {
		e(&c)
	}
	return c
}

type recorder struct {
	mu       sync.Mutex
	triggers [][]string
}

func (r *recorder) get() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.triggers...)
}

func newWatcher(dir string, c config.WatchConfig) (*FileWatcher, *recorder) {
	rec := &recorder{}
	w := New(dir, c, Hooks{OnTrigger: func(f []string) {
		rec.mu.Lock()
		rec.triggers = append(rec.triggers, f)
		rec.mu.Unlock()
	}})
	return w, rec
}

func TestRootsUseTheStaticPrefix(t *testing.T) {
	cases := []struct {
		in   []string
		want []Root
	}{
		{[]string{"src/**"}, []Root{{"src", true}}},
		{[]string{"package.json"}, []Root{{"", false}}},
		{[]string{"src/main.go"}, []Root{{"src", false}}},
		{[]string{"**/*.go", "src/**"}, []Root{{"", true}}},
		{[]string{"src/**", "package.json"}, []Root{{"src", true}, {"", false}}},
	}
	for _, c := range cases {
		if got := Roots(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Roots(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMatchesPathsHonoursIgnoreAndDefaultIgnores(t *testing.T) {
	w, rec := newWatcher("/nowhere", cfg(func(c *config.WatchConfig) { c.Ignore = []string{"src/**/*.test.js"} }))
	for _, f := range []string{"README.md", "src/a.test.js", "src/.git/x", "src/file.swp"} {
		w.Touch(f)
	}
	time.Sleep(150 * time.Millisecond)
	if len(rec.get()) != 0 {
		t.Fatalf("triggers = %v", rec.get())
	}
	w.Touch("src/a.js")
	w.Touch("package.json")
	time.Sleep(150 * time.Millisecond)
	if got := rec.get(); !reflect.DeepEqual(got, [][]string{{"src/a.js", "package.json"}}) {
		t.Fatalf("triggers = %v", got)
	}
	w.Close()
}

func TestABurstIsOneTrigger(t *testing.T) {
	w, rec := newWatcher("/nowhere", cfg(func(c *config.WatchConfig) { c.Debounce = 100 }))
	for i := 0; i < 5; i++ {
		w.Touch("src/f" + string(rune('0'+i)) + ".js")
		time.Sleep(60 * time.Millisecond) // always inside the debounce window
	}
	if len(rec.get()) != 0 {
		t.Fatal("fired inside the debounce window")
	}
	time.Sleep(200 * time.Millisecond)
	got := rec.get()
	if len(got) != 1 || len(got[0]) != 5 {
		t.Fatalf("triggers = %v", got)
	}
	w.Close()
}

func TestCooldownDelaysAndBatchesTheNextTrigger(t *testing.T) {
	w, rec := newWatcher("/nowhere", cfg(func(c *config.WatchConfig) { c.Debounce = 30; c.Cooldown = 300 }))
	w.Touch("src/a.js")
	time.Sleep(100 * time.Millisecond)
	if len(rec.get()) != 1 {
		t.Fatalf("first trigger: %v", rec.get())
	}
	w.Touch("src/b.js")
	time.Sleep(80 * time.Millisecond)
	w.Touch("src/c.js")
	time.Sleep(100 * time.Millisecond)
	if len(rec.get()) != 1 {
		t.Fatal("fired while cooling down")
	}
	time.Sleep(300 * time.Millisecond)
	want := [][]string{{"src/a.js"}, {"src/b.js", "src/c.js"}}
	if got := rec.get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("triggers = %v, want %v", got, want)
	}
	w.Close()
}

func TestPausedDropsTheQueue(t *testing.T) {
	w, rec := newWatcher("/nowhere", cfg())
	w.Touch("src/a.js")
	w.SetPaused(true)
	w.Touch("src/b.js")
	time.Sleep(150 * time.Millisecond)
	if len(rec.get()) != 0 || w.Pending() {
		t.Fatalf("triggers = %v, pending = %v", rec.get(), w.Pending())
	}
	w.SetPaused(false)
	w.Touch("src/c.js")
	time.Sleep(150 * time.Millisecond)
	if got := rec.get(); !reflect.DeepEqual(got, [][]string{{"src/c.js"}}) {
		t.Fatalf("triggers = %v", got)
	}
	w.Close()
}

func TestSeesRealFileWrites(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src", "deep"), 0o755)
	w, rec := newWatcher(dir, cfg())
	w.Start()
	defer w.Close()
	time.Sleep(100 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "src", "deep", "x.go"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("1"), 0o644)
	time.Sleep(500 * time.Millisecond)
	got := rec.get()
	if len(got) != 1 || len(got[0]) != 1 || got[0][0] != "src/deep/x.go" {
		t.Fatalf("triggers = %v", got)
	}
}

func TestSeesFilesInNewDirectories(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	w, rec := newWatcher(dir, cfg())
	w.Start()
	defer w.Close()
	time.Sleep(100 * time.Millisecond)
	os.MkdirAll(filepath.Join(dir, "src", "newdir"), 0o755)
	time.Sleep(150 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "src", "newdir", "y.js"), []byte("1"), 0o644)
	time.Sleep(500 * time.Millisecond)
	got := rec.get()
	if len(got) != 1 || got[0][0] != "src/newdir/y.js" {
		t.Fatalf("triggers = %v", got)
	}
}
