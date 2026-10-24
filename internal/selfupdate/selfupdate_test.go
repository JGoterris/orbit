package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"1.3.10", "1.3.2", true},
		{"1.3.2", "1.3.2", false},
		{"1.3.1", "1.3.2", false},
		{"1.4.0", "1.4.0-beta.1", true},
		{"1.4.0-beta.1", "1.4.0", false},
		{"2.0.0", "1.9.9", true},
		{"1.3.2", "dev", false},
		{"garbage", "1.0.0", false},
		{"v1.3.3", "1.3.2", true},
	} {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func fakeRun(t *testing.T, answers map[string]string) {
	t.Helper()
	old := runOut
	t.Cleanup(func() { runOut = old })
	runOut = func(name string, args ...string) (string, error) {
		if out, ok := answers[name]; ok {
			return out, nil
		}
		return "", errors.New("not found")
	}
}

func TestDetect(t *testing.T) {
	fakeRun(t, nil)
	if got := Detect("/home/linuxbrew/.linuxbrew/Cellar/orbit/1.3.2/bin/orbit", true); got.Method != Brew {
		t.Errorf("cellar: %v", got)
	}
	if got := Detect("/opt/x/orbit", true); got.Method != Binary {
		t.Errorf("loose binary: %v", got)
	}
	if got := Detect("/opt/x/orbit", false); got.Method != Dev && got.Method != GoInstall {
		t.Errorf("unstamped: %v", got)
	}
	if runtime.GOOS != "linux" {
		return
	}
	fakeRun(t, map[string]string{"dpkg": "orbit: /usr/bin/orbit", "dpkg-query": "Jesús Goterris <contact@jgoterris.com>"})
	if got := Detect("/usr/bin/orbit", true); got.Method != Deb {
		t.Errorf("our deb: %v", got)
	}
	fakeRun(t, map[string]string{"dpkg": "orbit: /usr/bin/orbit", "dpkg-query": "Someone Else <x@y.z>"})
	if got := Detect("/usr/bin/orbit", true); got.Method != Foreign || got.Detail != "orbit" {
		t.Errorf("foreign deb: %v", got)
	}
	fakeRun(t, map[string]string{"rpm": "orbit", "rpm-vendor": ""})
	if got := Detect("/usr/bin/orbit", true); got.Method != Foreign {
		t.Errorf("rpm without our vendor: %v", got)
	}
}

// release serves a fake release of orbit 9.9.9 whose binary contains content.
func release(t *testing.T, content string, sumOverride string) {
	t.Helper()
	archive, binName := archiveName("9.9.9")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: binName, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	tw.Write([]byte(content))
	tw.Close()
	gz.Close()
	sum := sha256.Sum256(buf.Bytes())
	hexsum := hex.EncodeToString(sum[:])
	if sumOverride != "" {
		hexsum = sumOverride
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v9.9.9/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", hexsum, archive)
	})
	mux.HandleFunc("/v9.9.9/"+archive, func(w http.ResponseWriter, _ *http.Request) { w.Write(buf.Bytes()) })
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"tag_name":"v9.9.9"}`) })
	srv := httptest.NewServer(mux)
	oldD, oldA := downloadBase, apiBase
	downloadBase, apiBase = srv.URL, srv.URL
	t.Cleanup(func() { srv.Close(); downloadBase, apiBase = oldD, oldA })
}

func TestReplaceBinary(t *testing.T) {
	release(t, "new orbit", "")
	exe := filepath.Join(t.TempDir(), "orbit")
	os.WriteFile(exe, []byte("old orbit"), 0o755)
	if err := ReplaceBinary(context.Background(), "9.9.9", exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new orbit" {
		t.Fatalf("binary = %q", got)
	}
}

func TestReplaceBinaryBadChecksum(t *testing.T) {
	release(t, "new orbit", strings.Repeat("0", 64))
	exe := filepath.Join(t.TempDir(), "orbit")
	os.WriteFile(exe, []byte("old orbit"), 0o755)
	if err := ReplaceBinary(context.Background(), "9.9.9", exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old orbit" {
		t.Fatalf("original was touched: %q", got)
	}
}

func TestLatestAndCache(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		fmt.Fprint(w, `{"tag_name":"v9.9.9"}`)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	if v, err := Latest(context.Background()); err != nil || v != "9.9.9" {
		t.Fatalf("Latest = %q, %v", v, err)
	}
	hits = 0
	RefreshIfStale(context.Background())
	if hits != 1 || Cached() != "9.9.9" {
		t.Fatalf("first refresh: hits=%d cached=%q", hits, Cached())
	}
	RefreshIfStale(context.Background())
	if hits != 1 {
		t.Fatalf("a fresh cache asked again: hits=%d", hits)
	}
}
