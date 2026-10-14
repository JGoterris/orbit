package projects

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/JGoterris/orbit/internal/userconfig"
)

func home(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return userconfig.Dir()
}

func names(list []Entry) []string {
	out := make([]string, len(list))
	for i, p := range list {
		out[i] = p.Name
	}
	return out
}

func TestRegistersKeepsPinAndSorts(t *testing.T) {
	home(t)
	Register("/a", "a", 100)
	Register("/b", "b", 200)
	Register("/c", "c", 300)
	if got := names(Read()); !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Fatalf("order = %v", got)
	}
	SetPinned("/a", true)
	Register("/a", "a", 400) // reopening must not unpin it
	if got := names(Sort(Read())); !reflect.DeepEqual(got, []string{"a", "c", "b"}) {
		t.Fatalf("sorted = %v", got)
	}
	var a Entry
	for _, p := range Read() {
		if p.Path == "/a" {
			a = p
		}
	}
	if a != (Entry{Path: "/a", Name: "a", LastOpened: 400, Pinned: true}) {
		t.Fatalf("entry = %+v", a)
	}
	SetPinned("/a", false)
	for _, p := range Read() {
		if p.Path == "/a" && p.Pinned {
			t.Fatal("still pinned")
		}
	}
}

func TestForgetAndTheRecentCap(t *testing.T) {
	home(t)
	Register("/keep", "keep", 1)
	SetPinned("/keep", true)
	for i := 0; i < 40; i++ {
		Register("/p"+string(rune('A'+i%26))+string(rune('a'+i/26)), "p", int64(100+i))
	}
	list := Read()
	unpinned, hasKeep := 0, false
	for _, p := range list {
		if p.Pinned {
			hasKeep = hasKeep || p.Path == "/keep"
		} else {
			unpinned++
		}
	}
	if unpinned != 30 || !hasKeep {
		t.Fatalf("unpinned = %d, keep = %v", unpinned, hasKeep)
	}
	Forget(list[len(list)-1].Path)
	if len(Read()) != len(list)-1 {
		t.Fatal("forget did not remove it")
	}
}

func TestCorruptRegistryReadsAsEmpty(t *testing.T) {
	dir := home(t)
	if Read() != nil {
		t.Fatal("missing file must be empty")
	}
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "projects.json"), []byte("{nope"), 0o644)
	if Read() != nil {
		t.Fatal("corrupt file must be empty")
	}
	os.WriteFile(filepath.Join(dir, "projects.json"), []byte(`{"projects":[null,{"path":3},{"path":"/ok","name":"ok"}]}`), 0o644)
	if got := Read(); !reflect.DeepEqual(got, []Entry{{Path: "/ok", Name: "ok"}}) {
		t.Fatalf("entries = %+v", got)
	}
}

func TestPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX path assumptions")
	}
	for _, p := range []string{"/x", "~", "~/x", "./x", "../x", ".."} {
		if !LooksLikePath(p) {
			t.Errorf("%q should look like a path", p)
		}
	}
	for _, p := range []string{"orbit", "my-api", ".hidden"} {
		if LooksLikePath(p) {
			t.Errorf("%q should be a search", p)
		}
	}
	h, _ := os.UserHomeDir()
	if got := ExpandPath("~/code", ""); got != filepath.Join(h, "code") {
		t.Errorf("~/code = %s", got)
	}
	if got := ExpandPath("x", "/base"); got != "/base/x" {
		t.Errorf("x = %s", got)
	}
}

func TestCompletePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX path assumptions")
	}
	root := t.TempDir()
	for _, d := range []string{"alpha", "alpine", "beta", ".git"} {
		os.Mkdir(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "alfile"), nil, 0o644)
	cases := map[string]string{
		root + "/be":     root + "/beta/",
		root + "/al":     root + "/alp", // alpha + alpine; the file "alfile" is not a folder
		root + "/zzz":    root + "/zzz",
		root + "/.g":     root + "/.git/",
		root + "/":       root + "/", // alpha, alpine, beta: nothing in common
		root + "/nope/x": root + "/nope/x",
	}
	for in, want := range cases {
		if got := CompletePath(in, ""); got != want {
			t.Errorf("CompletePath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CompletePath("rel", root); got != "rel" {
		t.Errorf("rel = %q", got)
	}
	os.Mkdir(filepath.Join(root, "relative"), 0o755)
	if got := CompletePath("rel", root); got != "relative/" {
		t.Errorf("rel = %q", got)
	}
	if got := CompletePath("./rel", root); got != "./relative/" {
		t.Errorf("./rel = %q", got)
	}
}

func TestStatusOfAMissingFolder(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if st := StatusOf(Entry{Path: "/definitely/not/here", Name: "x"}); st.Exists || st.Running != 0 || st.OpenIn != 0 {
		t.Fatalf("status = %+v", st)
	}
	if st := StatusOf(Entry{Path: t.TempDir(), Name: "x"}); !st.Exists {
		t.Fatalf("status = %+v", st)
	}
}

func TestFind(t *testing.T) {
	list := []Entry{
		{Path: "/w/shop-api", Name: "shop-api"}, {Path: "/w/shop-web", Name: "shop-web"},
		{Path: "/x/blog", Name: "blog"}, {Path: "/y/blog", Name: "blog"}, {Path: "/z/docs", Name: "docs"},
	}
	byName := map[string]string{"docs": "/z/docs", "DOC": "/z/docs"}
	if runtime.GOOS != "windows" {
		byName["/w/shop-web"] = "/w/shop-web" // a path: only meaningful where "/w" is absolute
	}
	for q, want := range byName {
		if got, err := Find(q, list); err != nil || got.Path != want {
			t.Errorf("Find(%q) = %+v, %v", q, got, err)
		}
	}
	for q, want := range map[string]string{"blog": "/x/blog, /y/blog", "shop": "shop-api, shop-web", "nothing": "no project"} {
		if _, err := Find(q, list); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Find(%q) err = %v, want %q", q, err, want)
		}
	}
	if _, err := Find("x", nil); err == nil || !strings.Contains(err.Error(), "orbit projects") {
		t.Errorf("empty registry: %v", err)
	}
}
