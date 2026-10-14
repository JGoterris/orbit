package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func need(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func newRepo(t *testing.T) string {
	t.Helper()
	need(t)
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	sh(t, dir, "config", "user.name", "t")
	sh(t, dir, "config", "user.email", "t@t")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseStatus(t *testing.T) {
	out := "# branch.oid abc1234def\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +2 -1\x00" +
		"1 .M N... 100644 100644 100644 aaa bbb src/a.go\x00" +
		"1 A. N... 000000 100644 100644 000 bbb new file.txt\x00" +
		"2 R. N... 100644 100644 100644 aaa bbb R100 new.go\x00old.go\x00" +
		"u UU N... 100644 100644 100644 100644 a b c conflict.go\x00" +
		"? untracked.txt\x00"
	st := ParseStatus(out)
	if st.Branch != "main" || st.Oid != "abc1234" || st.Upstream != "origin/main" || st.Ahead != 2 || st.Behind != 1 || !st.HasCommits {
		t.Fatalf("branch info = %+v", st)
	}
	want := []FileChange{
		{Path: "src/a.go", X: '.', Y: 'M', Kind: Tracked},
		{Path: "new file.txt", X: 'A', Y: '.', Kind: Tracked},
		{Path: "new.go", Orig: "old.go", X: 'R', Y: '.', Kind: Tracked},
		{Path: "conflict.go", X: 'U', Y: 'U', Kind: Conflict},
		{Path: "untracked.txt", X: '.', Y: '?', Kind: Untracked},
	}
	if !reflect.DeepEqual(st.Files, want) {
		t.Fatalf("files = %+v", st.Files)
	}
	if !want[1].HasStaged() || want[1].HasUnstaged() || !want[0].HasUnstaged() || !want[4].HasUnstaged() {
		t.Error("staged / unstaged flags")
	}
}

func TestParseStatusInitialAndDetached(t *testing.T) {
	st := ParseStatus("# branch.oid (initial)\x00# branch.head main\x00")
	if st.HasCommits || st.Oid != "" || st.Branch != "main" {
		t.Fatalf("initial = %+v", st)
	}
	st = ParseStatus("# branch.oid 1234567890\x00# branch.head (detached)\x00")
	if st.Branch != "" || st.Oid != "1234567" {
		t.Fatalf("detached = %+v", st)
	}
}

func TestParseDiff(t *testing.T) {
	patch := "diff --git a/x.go b/x.go\nindex 1..2 100644\n--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,3 @@ func\n a\n-b\n+c\n+d\n" +
		"diff --git a/img.png b/img.png\nBinary files a/img.png and b/img.png differ\n" +
		"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\n--- a/gone.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n"
	files := ParseDiff(patch)
	if len(files) != 3 {
		t.Fatalf("files = %d", len(files))
	}
	if files[0].Path != "x.go" || len(files[0].Hunks) != 1 || files[0].Hunks[0].OldStart != 1 || files[0].Hunks[0].NewStart != 1 {
		t.Fatalf("file 0 = %+v", files[0])
	}
	if got := files[0].Hunks[0].Lines; !reflect.DeepEqual(got, []string{" a", "-b", "+c", "+d"}) {
		t.Fatalf("lines = %q", got)
	}
	if !files[1].Binary || files[1].Path != "img.png" {
		t.Fatalf("binary = %+v", files[1])
	}
	if files[2].Path != "gone.txt" {
		t.Fatalf("deleted path = %q", files[2].Path)
	}
}

func TestBuildTreeMergesSingleChildFoldersAndFolds(t *testing.T) {
	files := []FileChange{
		{Path: "README.md"}, {Path: "src/ui/views/a.go"}, {Path: "src/ui/views/b.go"}, {Path: "src/core/x.go"}, {Path: "z.txt"},
	}
	rows := BuildTree(files, nil)
	var got []string
	for _, r := range rows {
		kind := "f"
		if r.Dir {
			kind = "d"
		}
		got = append(got, kind+":"+r.Name+":"+itoa(r.Depth))
	}
	want := []string{"d:src:0", "d:core:1", "f:x.go:2", "d:ui/views:1", "f:a.go:2", "f:b.go:2", "f:README.md:0", "f:z.txt:0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v\nwant %v", got, want)
	}
	if rows[0].Collapsed || len(rows[0].Files) != 3 {
		t.Fatalf("src dir = %+v", rows[0])
	}
	folded := BuildTree(files, map[string]bool{"src": true})
	if len(folded) != 3 || !folded[0].Collapsed {
		t.Fatalf("folded = %d rows", len(folded))
	}
}

func TestParsers(t *testing.T) {
	log := "* \x1fabc123\x1fAda\x1f2 days ago\x1fHEAD -> main, tag: v1\x1ffix: thing\n| \n* \x1fdef456\x1fBob\x1f3 days ago\x1f\x1finitial\n"
	commits := ParseLog(log)
	if len(commits) != 2 || commits[0].Hash != "abc123" || commits[0].Refs != "HEAD -> main, tag: v1" || commits[1].Subject != "initial" || commits[0].Graph != "* " {
		t.Fatalf("commits = %+v", commits)
	}
	br := ParseBranches("*\x1fmain\x1forigin/main\x1f[ahead 1]\x1f2 hours ago\n \x1ffeature\x1f\x1f\x1f1 day ago\n")
	if len(br) != 2 || !br[0].Current || br[0].Track != "ahead 1" || br[1].Upstream != "" {
		t.Fatalf("branches = %+v", br)
	}
	st := ParseStashes("stash@{0}\x1fabc\x1fWIP on main\nstash@{1}\x1fdef\x1fOn x: y\n")
	if len(st) != 2 || st[0].Ref != "stash@{0}" || st[1].Message != "On x: y" {
		t.Fatalf("stashes = %+v", st)
	}
}

func TestFormatArgs(t *testing.T) {
	if got := FormatArgs([]string{"commit", "-m", "a b", "x"}); got != `commit -m "a b" x` {
		t.Fatalf("FormatArgs = %q", got)
	}
}

func TestStageCommitAndBranchesOnARealRepo(t *testing.T) {
	dir := newRepo(t)
	Commands.Clear()
	write(t, dir, "a.txt", "one\n")
	write(t, dir, "sub/b.txt", "two\n")
	st, ok := ReadStatus(dir)
	if !ok || st.HasCommits || len(st.Files) != 2 || st.Files[0].Kind != Untracked {
		t.Fatalf("status = %+v ok=%v", st, ok)
	}
	if res := Stage(dir, []string{"a.txt"}); !res.OK {
		t.Fatalf("stage: %+v", res)
	}
	st, _ = ReadStatus(dir)
	var staged int
	for _, f := range st.Files {
		if f.HasStaged() {
			staged++
		}
	}
	if staged != 1 {
		t.Fatalf("staged = %d", staged)
	}
	// unstage in a repo without commits uses rm --cached
	if res := Unstage(dir, []string{"a.txt"}, false); !res.OK {
		t.Fatalf("unstage: %+v", res)
	}
	if res := StageAll(dir); !res.OK {
		t.Fatalf("stage all: %+v", res)
	}
	if res := Commit(dir, "first commit"); !res.OK {
		t.Fatalf("commit: %+v", res)
	}
	st, _ = ReadStatus(dir)
	if !st.HasCommits || len(st.Files) != 0 || st.Branch != "main" {
		t.Fatalf("after commit = %+v", st)
	}
	commits := Log(dir, 10)
	if len(commits) != 1 || commits[0].Subject != "first commit" {
		t.Fatalf("log = %+v", commits)
	}
	if !strings.Contains(CommitDiff(dir, commits[0].Hash), "+one") {
		t.Fatal("commit diff misses the added line")
	}

	if res := CreateBranch(dir, "feature"); !res.OK {
		t.Fatalf("create branch: %+v", res)
	}
	if res := Checkout(dir, "main"); !res.OK {
		t.Fatalf("checkout: %+v", res)
	}
	names := []string{}
	for _, b := range Branches(dir) {
		names = append(names, b.Name)
		if b.Name == "main" && !b.Current {
			t.Error("main must be current")
		}
	}
	if len(names) != 2 {
		t.Fatalf("branches = %v", names)
	}
	if res := Checkout(dir, "no-such-branch"); res.OK || res.Message == "" {
		t.Fatalf("a bad checkout must fail with git's words: %+v", res)
	}

	entries := Commands.Entries()
	if len(entries) < 6 || !entries[0].OK || entries[0].Args[0] != "add" {
		t.Fatalf("command log = %+v", entries)
	}
	last := entries[len(entries)-1]
	if last.OK || last.Args[0] != "switch" {
		t.Fatalf("last logged command = %+v", last)
	}
}

func TestFileDiffSides(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "f.txt", "v1\n")
	sh(t, dir, "add", "-A")
	sh(t, dir, "commit", "-q", "-m", "init")
	write(t, dir, "f.txt", "v2\n")
	f := FileChange{Path: "f.txt", X: '.', Y: 'M', Kind: Tracked}
	if d := FileDiff(dir, f, Unstaged); !strings.Contains(d, "-v1") || !strings.Contains(d, "+v2") {
		t.Fatalf("unstaged diff = %q", d)
	}
	sh(t, dir, "add", "f.txt")
	if d := FileDiff(dir, f, Staged); !strings.Contains(d, "+v2") {
		t.Fatalf("staged diff = %q", d)
	}
	write(t, dir, "new.txt", "hello\n")
	d := FileDiff(dir, FileChange{Path: "new.txt", Kind: Untracked}, Unstaged)
	if !strings.Contains(d, "+hello") {
		t.Fatalf("untracked diff = %q", d)
	}
}

func TestRepoRefreshAndDiscover(t *testing.T) {
	a, b := newRepo(t), newRepo(t)
	write(t, a, "x", "1")
	write(t, filepath.Join(b, "svc"), "y", "1")
	names := []string{"api", "web", "plain"}
	cwds := map[string]string{"api": a, "web": filepath.Join(b, "svc"), "plain": t.TempDir()}
	entries := Discover(a, names, func(n string) string { return cwds[n] })
	if len(entries) != 2 || entries[0].Repo.Root != a || entries[1].Repo.Root != b {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Services[0] != "api" || RepoOfService(entries, "web") != 1 || RepoOfService(entries, "plain") != -1 {
		t.Fatalf("services = %+v", entries)
	}
	r := entries[0].Repo
	r.Refresh("all")
	snap := r.Snapshot()
	if snap.Status == nil || len(snap.Status.Files) != 1 || snap.Status.Branch != "main" {
		t.Fatalf("snapshot = %+v", snap)
	}
	v := snap.Version
	r.Refresh("all")
	if r.Snapshot().Version != v {
		t.Error("an unchanged refresh must not bump the version")
	}

	res := RunOnRepos(entries, func(e Entry) OpResult { return Stage(e.Repo.Root, []string{"."}) })
	sum := Summarize("staged", res)
	if !sum.OK || sum.Message != "staged 2 repos" {
		t.Fatalf("summary = %+v", sum)
	}
	spread := BranchSpread(entries)
	if len(spread) != 1 || spread[0].Name != "main" || spread[0].Count != 2 {
		t.Fatalf("spread = %+v", spread)
	}
	failed := Summarize("pulled", []RepoResult{{entries[0], OpResult{OK: true}}, {entries[1], OpResult{Message: "boom"}}})
	if failed.OK || !strings.Contains(failed.Message, "pulled 1/2") || !strings.Contains(failed.Message, "boom") {
		t.Fatalf("failed summary = %+v", failed)
	}
}

func TestSameNamedReposGetTheirParent(t *testing.T) {
	need(t)
	root := t.TempDir()
	var repos []*Repo
	for _, p := range []string{"a/app", "b/app"} {
		d := filepath.Join(root, p)
		os.MkdirAll(d, 0o755)
		sh(t, d, "init", "-q")
		repos = append(repos, NewRepo(d))
	}
	e := EntriesOf(repos)
	if e[0].Name != "a/app" || e[1].Name != "b/app" {
		t.Fatalf("names = %q, %q", e[0].Name, e[1].Name)
	}
}
