package plugins

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testManifest = `name: demo
version: 1.2.0
api: 1
run: { command: [./demo] }
`

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func writePlugin(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(dir, "orbit-plugin.yaml"), []byte(testManifest), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "demo"), []byte("#!/bin/sh\n"), 0o755))
	must(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("x"), 0o644))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallLocalCopiesWithoutGit(t *testing.T) {
	isolate(t)
	src := filepath.Join(t.TempDir(), "demo-src")
	writePlugin(t, src)

	st, err := Stage(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Root(), "demo")); err == nil {
		t.Fatal("staging must not install anything")
	}
	p, err := st.Commit()
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.Name != "demo" || p.Meta.Version != "1.2.0" || p.Meta.Source != src {
		t.Fatalf("installed = %+v", p)
	}
	if _, err := os.Stat(filepath.Join(Root(), "demo", ".git")); err == nil {
		t.Fatal(".git must not be copied")
	}
	if st, err := os.Stat(filepath.Join(Root(), "demo", "demo")); err != nil || st.Mode()&0o111 == 0 && runtime.GOOS != "windows" {
		t.Fatalf("the executable bit must survive: %v %v", st, err)
	}
	list, broken := Installed()
	if len(list) != 1 || len(broken) != 0 {
		t.Fatalf("installed = %v broken = %v", list, broken)
	}
	entries, _ := os.ReadDir(Root())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".staging") {
			t.Fatalf("a staging folder was left behind: %s", e.Name())
		}
	}
}

func TestStageDiscardAndNotAPlugin(t *testing.T) {
	isolate(t)
	empty := t.TempDir()
	if _, err := Stage(context.Background(), empty); err == nil || !strings.Contains(err.Error(), "orbit-plugin.yaml") {
		t.Fatalf("a folder without a manifest must be refused: %v", err)
	}
	if _, err := Stage(context.Background(), "not a spec"); err == nil {
		t.Fatal("garbage must be refused")
	}
	src := filepath.Join(t.TempDir(), "p")
	writePlugin(t, src)
	st, _ := Stage(context.Background(), src)
	st.Discard()
	if entries, _ := os.ReadDir(Root()); len(entries) != 0 {
		t.Fatalf("Discard left %d entries", len(entries))
	}
}

func TestLinkFollowsTheSourceFolder(t *testing.T) {
	isolate(t)
	src := filepath.Join(t.TempDir(), "dev")
	writePlugin(t, src)
	if _, err := Link(src); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(src, "orbit-plugin.yaml"), []byte(strings.Replace(testManifest, "1.2.0", "1.3.0", 1)), 0o644))
	p, err := Load("demo")
	if err != nil || p.Manifest.Version != "1.3.0" || p.Dir != src || !p.Meta.Linked {
		t.Fatalf("a linked plugin must be read from its folder: %+v %v", p, err)
	}
	st, err := Upgrade(context.Background(), p)
	if st != nil || err != nil {
		t.Fatal("a linked plugin has nothing to upgrade")
	}
	must(t, Remove("demo"))
	if _, err := os.Stat(filepath.Join(src, "orbit-plugin.yaml")); err != nil {
		t.Fatal("removing a link must keep the source folder")
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		must(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		must(t, err)
	}
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		must(t, err)
		_, err = w.Write([]byte(body))
		must(t, err)
	}
	must(t, zw.Close())
	return buf.Bytes()
}

// fakeGitHub serves one release of owner/orbit-demo; sums overrides the contents of checksums.txt.
func fakeGitHub(t *testing.T, archive []byte, sums *string) {
	t.Helper()
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	asset := fmt.Sprintf("orbit-demo_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext)
	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)
	if sums != nil {
		checksums = *sums
	}
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/owner/orbit-demo/releases/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.2.0","assets":[{"name":%q,"browser_download_url":"%s/dl/%s"},{"name":"checksums.txt","browser_download_url":"%s/dl/checksums.txt"}]}`,
			asset, srv.URL, asset, srv.URL)
	})
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, checksums) })
	mux.HandleFunc("/dl/"+asset, func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv = httptest.NewServer(mux)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old; srv.Close() })
}

func releaseArchive(t *testing.T, files map[string]string) []byte {
	if runtime.GOOS == "windows" {
		return zipOf(t, files)
	}
	return tarGz(t, files)
}

func TestInstallFromRelease(t *testing.T) {
	isolate(t)
	fakeGitHub(t, releaseArchive(t, map[string]string{"orbit-plugin.yaml": testManifest, "demo": "bin"}), nil)
	st, err := Stage(context.Background(), "owner/orbit-demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Warnings) != 0 {
		t.Fatalf("a verified release has nothing to warn about: %v", st.Warnings)
	}
	p, err := st.Commit()
	if err != nil || p.Meta.Source != "owner/orbit-demo" {
		t.Fatalf("%+v %v", p, err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.Dir, "demo")); string(b) != "bin" {
		t.Fatal("the binary was not unpacked")
	}
}

func TestInstallFromReleaseWithAFolderInside(t *testing.T) {
	isolate(t)
	fakeGitHub(t, releaseArchive(t, map[string]string{"orbit-demo_1.2.0/orbit-plugin.yaml": testManifest, "orbit-demo_1.2.0/demo": "bin"}), nil)
	st, err := Stage(context.Background(), "owner/orbit-demo@v1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Discard()
	if st.Plugin.Meta.Ref != "v1.2.0" {
		t.Fatalf("the pinned tag must be remembered: %+v", st.Plugin.Meta)
	}
}

func TestInstallRejectsABadChecksum(t *testing.T) {
	isolate(t)
	bad := strings.Repeat("0", 64) + "  whatever\n"
	fakeGitHub(t, releaseArchive(t, map[string]string{"orbit-plugin.yaml": testManifest}), &bad)
	if _, err := Stage(context.Background(), "owner/orbit-demo"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want a checksum error, got %v", err)
	}
	if entries, _ := os.ReadDir(Root()); len(entries) != 0 {
		t.Fatalf("a failed install must clean up: %d entries", len(entries))
	}
}

func TestInstallRejectsEscapingArchives(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"dotdot":   {"../evil": "x", "orbit-plugin.yaml": testManifest},
		"absolute": {"/tmp/evil": "x", "orbit-plugin.yaml": testManifest},
	} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			for _, arc := range []struct {
				name string
				data []byte
			}{{"x.tar.gz", tarGz(t, files)}, {"x.zip", zipOf(t, files)}} {
				err := extractArchive(arc.data, arc.name, dest)
				if err == nil || !strings.Contains(err.Error(), "outside") {
					t.Fatalf("%s: want an escape error, got %v", arc.name, err)
				}
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "evil")); err == nil {
				t.Fatal("a file was written outside the destination")
			}
		})
	}
}

func TestInstallRejectsLinksInArchives(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	must(t, tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}))
	tw.Close()
	gz.Close()
	if err := extractArchive(buf.Bytes(), "x.tar.gz", t.TempDir()); err == nil {
		t.Fatal("a symlink in an archive must be refused")
	}
}

func TestInstallFallsBackToCloning(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	isolate(t)
	// a repository without releases: the fake API answers 404 and the clone comes from a local repo
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	oldAPI, oldClone := apiBase, cloneBase
	apiBase, cloneBase = srv.URL, t.TempDir()
	t.Cleanup(func() { apiBase, cloneBase = oldAPI, oldClone })

	repo := filepath.Join(cloneBase, "owner", "orbit-demo.git")
	writePlugin(t, repo)
	must(t, os.RemoveAll(filepath.Join(repo, ".git"))) // the fake one of writePlugin
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	st, err := Stage(context.Background(), "owner/orbit-demo")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Discard()
	if len(st.Warnings) == 0 {
		t.Fatal("a clone is not verified and must say so")
	}
	if _, err := os.Stat(filepath.Join(st.Plugin.Dir, ".git")); err == nil {
		t.Fatal(".git must be removed")
	}
}
