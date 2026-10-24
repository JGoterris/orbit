// Package selfupdate finds out whether a newer orbit exists and updates the running binary through the channel it
// was installed with (Homebrew, the .deb / .rpm packages, `go install`, or a plain binary from GitHub Releases).
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/userconfig"
)

// Other packages are also called "orbit": every command spells out which one is ours.
const (
	brewFormula = "JGoterris/tap/orbit"
	goPackage   = "github.com/JGoterris/orbit/cmd/orbit"
)

// The endpoints are variables so the tests can point them at a local server.
var (
	apiBase      = "https://api.github.com/repos/JGoterris/orbit"
	downloadBase = "https://github.com/JGoterris/orbit/releases/download"
	httpClient   = &http.Client{Timeout: 60 * time.Second}
)

// maxDownload bounds what is read from the network (the archives are around 10 MB).
const maxDownload = 200 << 20

// Latest is the version (without the leading v) of the newest final release; pre-releases are not offered.
func Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "orbit-update")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var body struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	v := strings.TrimPrefix(body.Tag, "v")
	if _, _, ok := parse(v); !ok {
		return "", fmt.Errorf("unexpected release tag %q", body.Tag)
	}
	return v, nil
}

// parse reads X.Y.Z[-suffix]; ok is false for anything else ("dev" included).
func parse(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(v, "v")
	core, pre, _ := strings.Cut(v, "-")
	core, _, _ = strings.Cut(core, "+")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return nums, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, "", false
		}
		nums[i] = n
	}
	return nums, pre, true
}

// Newer reports whether latest is a later version than current. A pre-release is older than its final version;
// an unparsable version (a "dev" build) is never compared.
func Newer(latest, current string) bool {
	ln, lpre, ok := parse(latest)
	if !ok {
		return false
	}
	cn, cpre, ok := parse(current)
	if !ok {
		return false
	}
	for i := range ln {
		if ln[i] != cn[i] {
			return ln[i] > cn[i]
		}
	}
	return cpre != "" && lpre == ""
}

// Method is how the running binary got onto the machine.
type Method int

const (
	// Binary is a plain executable (GitHub Releases): orbit replaces it itself.
	Binary Method = iota
	Brew
	// Deb and Rpm are system packages: orbit prints the command, it does not ask for sudo.
	Deb
	Rpm
	GoInstall
	// Foreign is a system package called orbit that is not ours: nothing is touched.
	Foreign
	// Dev is a local build without a version.
	Dev
)

// Install is the result of Detect.
type Install struct {
	Method Method
	// Detail names the package that owns the binary when Method is Foreign.
	Detail string
}

func (m Method) String() string {
	return [...]string{"binary", "Homebrew", "apt (.deb)", "rpm", "go install", "another package", "local build"}[m]
}

// runOut runs a command and returns its stdout; the tests replace it.
var runOut = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// Detect works out how the binary at exe (symlinks already resolved) was installed. stamped is whether the
// build carries a release version (-X main.version), which `go install` and `go build` do not.
func Detect(exe string, stamped bool) Install {
	slash := filepath.ToSlash(exe)
	if strings.Contains(slash, "/Cellar/orbit/") {
		return Install{Method: Brew}
	}
	if !stamped {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			return Install{Method: GoInstall}
		}
		return Install{Method: Dev}
	}
	if runtime.GOOS == "linux" {
		if out, err := runOut("dpkg", "-S", exe); err == nil {
			pkg, _, _ := strings.Cut(out, ":")
			return ownedBy(Deb, strings.TrimSpace(pkg), func(pkg string) string {
				maint, _ := runOut("dpkg-query", "-W", "-f=${Maintainer}", pkg)
				return maint
			})
		}
		if out, err := runOut("rpm", "-qf", "--qf", "%{NAME}", exe); err == nil {
			return ownedBy(Rpm, out, func(pkg string) string {
				vendor, _ := runOut("rpm", "-q", "--qf", "%{VENDOR}", pkg)
				return vendor
			})
		}
	}
	return Install{Method: Binary}
}

// ownedBy accepts a system package only when it is named orbit and made by us.
func ownedBy(m Method, pkg string, maker func(string) string) Install {
	if pkg == "orbit" && strings.Contains(maker(pkg), "Goterris") {
		return Install{Method: m}
	}
	return Install{Method: Foreign, Detail: pkg}
}

// Update brings the install up to version. It returns the line to show when the user has to finish the job
// (system packages need sudo), "" when it did everything itself.
func Update(ctx context.Context, in Install, exe, version string, stdout, stderr io.Writer) (string, error) {
	switch in.Method {
	case Brew:
		// `brew update` refreshes the tap; without it the formula may still be the old one
		if err := run(ctx, stdout, stderr, "brew", "update"); err != nil {
			return "", err
		}
		return "", run(ctx, stdout, stderr, "brew", "upgrade", brewFormula)
	case Deb:
		return "sudo apt update && sudo apt install --only-upgrade orbit", nil
	case Rpm:
		return "sudo dnf upgrade orbit", nil
	case GoInstall:
		return "", run(ctx, stdout, stderr, "go", "install", goPackage+"@latest")
	case Foreign:
		return "", fmt.Errorf("this orbit belongs to the package %q, which is not the one from JGoterris: update it with its own package manager", in.Detail)
	case Dev:
		return "", errors.New("this is a local build without a version: rebuild it from the repository")
	}
	return "", ReplaceBinary(ctx, version, exe)
}

func run(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func archiveName(version string) (archive, binary string) {
	ext, binary := ".tar.gz", "orbit"
	if runtime.GOOS == "windows" {
		ext, binary = ".zip", "orbit.exe"
	}
	return fmt.Sprintf("orbit_%s_%s_%s%s", version, runtime.GOOS, runtime.GOARCH, ext), binary
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "orbit-update")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownload {
		return nil, fmt.Errorf("%s: too large", url)
	}
	return data, nil
}

// ReplaceBinary downloads the release archive of version, checks it against checksums.txt and puts the new
// executable where exe is. The original stays untouched if anything fails.
func ReplaceBinary(ctx context.Context, version, exe string) error {
	archive, binName := archiveName(version)
	base := downloadBase + "/v" + version + "/"
	sums, err := download(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.TrimPrefix(f[1], "*") == archive {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", archive)
	}
	data, err := download(ctx, base+archive)
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("%s does not match its checksum: not installing it", archive)
	}
	bin, err := extract(data, archive, binName)
	if err != nil {
		return err
	}
	return swap(exe, bin)
}

// extract pulls the executable out of the archive.
func extract(data []byte, archive, binName string) ([]byte, error) {
	if strings.HasSuffix(archive, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == binName {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxDownload))
			}
		}
		return nil, fmt.Errorf("%s has no %s", archive, binName)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s has no %s", archive, binName)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && h.Name == binName {
			return io.ReadAll(io.LimitReader(tr, maxDownload))
		}
	}
}

// swap writes bin next to exe and renames it over. A running executable cannot be overwritten on Windows, but it
// can be renamed away first.
func swap(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".orbit-update-*")
	if err != nil {
		return fmt.Errorf("cannot write in %s (%w): run it with sudo, or reinstall in a directory you own", dir, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(name, exe); err != nil {
			os.Rename(old, exe)
			return err
		}
		return nil
	}
	return os.Rename(name, exe)
}

// CleanOld removes the executable a previous update renamed away on Windows.
func CleanOld(exe string) { os.Remove(exe + ".old") }

// ── the once-a-day check behind the TUI notice ──────────────────────────────────────────────────

type cacheFile struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

// checkEvery is how often the TUI may ask GitHub.
const checkEvery = 24 * time.Hour

func cachePath() string { return filepath.Join(platform.StateBase(), "orbit", "update-check.json") }

func readCache() cacheFile {
	var c cacheFile
	if data, err := os.ReadFile(cachePath()); err == nil {
		json.Unmarshal(data, &c)
	}
	return c
}

// Cached is the latest version the last check found, "" when there is none.
func Cached() string { return readCache().Latest }

// RefreshIfStale asks GitHub for the latest version when the cache is older than a day and stores the answer.
// A failed request is also remembered, so an offline machine does not retry on every launch.
func RefreshIfStale(ctx context.Context) {
	c := readCache()
	if time.Since(c.Checked) < checkEvery {
		return
	}
	if v, err := Latest(ctx); err == nil {
		c.Latest = v
	}
	c.Checked = time.Now()
	userconfig.WriteJSONAtomic(cachePath(), c)
}
