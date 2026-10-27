package plugins

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
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// apiBase is GitHub's API; a variable so the tests can point it at a fake one.
var apiBase = "https://api.github.com"

// cloneBase is where `git clone` gets repositories from; a variable for the same reason.
var cloneBase = "https://github.com"

const (
	maxDownload = 200 << 20
	maxUnpacked = 400 << 20
)

var repoSpec = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:@([A-Za-z0-9_.+-]+))?$`)

// Staged is a plugin downloaded or copied into a temporary folder, not installed yet: the caller shows what it
// does and asks before Commit.
type Staged struct {
	Plugin *Plugin
	// Warnings are things worth telling the user, such as a release without checksums
	Warnings []string
	tmp      string
}

// Stage fetches spec (a folder, or owner/repo[@tag] of GitHub) into a temporary folder and validates it.
func Stage(ctx context.Context, spec string) (*Staged, error) {
	if st, err := os.Stat(spec); err == nil && st.IsDir() {
		return stageLocal(spec)
	}
	m := repoSpec.FindStringSubmatch(spec)
	if m == nil {
		return nil, fmt.Errorf("%q is neither a folder nor owner/repo[@tag]", spec)
	}
	if strings.Trim(m[1], ".") == "" || strings.Trim(m[2], ".") == "" {
		return nil, fmt.Errorf("%q is not a valid owner/repo", spec)
	}
	return stageGitHub(ctx, m[1], m[2], m[3])
}

func newTemp() (string, error) {
	if err := os.MkdirAll(Root(), 0o755); err != nil {
		return "", err
	}
	sweepStaging()
	return os.MkdirTemp(Root(), ".staging-")
}

// sweepStaging deletes the staging folders an install that was killed left behind. The recent ones may belong to an
// install that is still running.
func sweepStaging() {
	entries, _ := os.ReadDir(Root())
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".staging-") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(Root(), e.Name()))
		}
	}
}

func stageLocal(src string) (*Staged, error) {
	abs, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}
	if _, err := ReadManifest(abs); err != nil {
		return nil, err
	}
	tmp, err := newTemp()
	if err != nil {
		return nil, err
	}
	if err := copyTree(abs, tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return finishStage(tmp, Meta{Source: abs}, nil)
}

func finishStage(tmp string, meta Meta, warnings []string) (*Staged, error) {
	dir, err := manifestRoot(tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	m, err := ReadManifest(dir)
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	meta.Version = m.Version
	return &Staged{Plugin: &Plugin{Dir: dir, Manifest: m, Meta: meta}, Warnings: warnings, tmp: tmp}, nil
}

// manifestRoot is the folder of tmp that holds the manifest: tmp itself, or the single folder an archive or a
// clone has inside it.
func manifestRoot(tmp string) (string, error) {
	if _, err := os.Stat(filepath.Join(tmp, "orbit-plugin.yaml")); err == nil {
		return tmp, nil
	}
	entries, _ := os.ReadDir(tmp)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) == 1 {
		if _, err := os.Stat(filepath.Join(tmp, dirs[0], "orbit-plugin.yaml")); err == nil {
			return filepath.Join(tmp, dirs[0]), nil
		}
	}
	return "", errors.New("it has no orbit-plugin.yaml: this is not an orbit plugin")
}

func (s *Staged) Commit() (*Plugin, error) {
	name := s.Plugin.Manifest.Name
	dest := filepath.Join(Root(), name)
	if err := os.RemoveAll(dest); err != nil {
		return nil, err
	}
	if err := os.Rename(s.Plugin.Dir, dest); err != nil {
		return nil, err
	}
	os.RemoveAll(s.tmp)
	if err := writeMeta(dest, s.Plugin.Meta); err != nil {
		return nil, err
	}
	return Load(name)
}

func (s *Staged) Discard() { os.RemoveAll(s.tmp) }

// Link registers a folder as a plugin without copying it, so edits show up at once: a plugin under development.
// Nothing is symlinked (that needs privileges on Windows): the installed folder only records where the plugin is.
func Link(src string) (*Plugin, error) {
	abs, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}
	m, err := ReadManifest(abs)
	if err != nil {
		return nil, err
	}
	dest := filepath.Join(Root(), m.Name)
	if err := os.RemoveAll(dest); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	if err := writeMeta(dest, Meta{Source: abs, Version: m.Version, Linked: true, Path: abs}); err != nil {
		return nil, err
	}
	return Load(m.Name)
}

// Upgrade installs the latest version of a plugin from where it came from. It returns nil, nil for a linked one.
func Upgrade(ctx context.Context, p *Plugin) (*Staged, error) {
	if p.Meta.Linked {
		return nil, nil
	}
	if p.Meta.Source == "" {
		return nil, errors.New("it does not remember where it came from: install it again")
	}
	spec := p.Meta.Source
	if p.Meta.Ref != "" {
		spec += "@" + p.Meta.Ref
	}
	return Stage(ctx, spec)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if d.Name() == ".git" || d.Name() == metaFile {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&os.ModeSymlink != 0:
			return nil
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm()|0o600)
		}
		return nil
	})
}

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func stageGitHub(ctx context.Context, owner, repo, ref string) (*Staged, error) {
	meta := Meta{Source: owner + "/" + repo, Ref: ref}
	tmp, err := newTemp()
	if err != nil {
		return nil, err
	}
	rel, err := findRelease(ctx, owner, repo, ref)
	var warnings []string
	if err == nil {
		var ok bool
		ok, warnings, err = unpackAsset(ctx, rel, repo, tmp)
		if err != nil {
			os.RemoveAll(tmp)
			return nil, err
		}
		if ok {
			return finishStage(tmp, meta, warnings)
		}
	} else if !errors.Is(err, errNoRelease) {
		os.RemoveAll(tmp)
		return nil, err
	}
	// no release, or none built for this platform: the repository itself may be a plugin made of scripts
	if err := gitClone(ctx, owner, repo, ref, tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return finishStage(tmp, meta, []string{"installed from the source of the repository, not from a release: nothing was checked"})
}

var errNoRelease = errors.New("no release")

func apiGet(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "orbit")
	req.Header.Set("Accept", "application/vnd.github+json")
	if t := os.Getenv("GITHUB_TOKEN"); t != "" && strings.HasPrefix(url, apiBase) {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(data) > maxDownload {
		return nil, resp.StatusCode, errors.New("download too large")
	}
	return data, resp.StatusCode, nil
}

func findRelease(ctx context.Context, owner, repo, ref string) (*release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBase, owner, repo)
	if ref != "" {
		url = fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", apiBase, owner, repo, ref)
	}
	data, status, err := apiGet(ctx, url)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, errNoRelease
	case status != http.StatusOK:
		return nil, fmt.Errorf("github answered %d for %s/%s", status, owner, repo)
	}
	var rel release
	if err := json.Unmarshal(data, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// assetName finds the archive of this platform: <repo>_<os>_<arch>.tar.gz (.zip on Windows), the names GoReleaser
// gives by default.
func (r *release) asset(repo string) (name, url string) {
	ext := ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	want := fmt.Sprintf("%s_%s_%s%s", repo, runtime.GOOS, runtime.GOARCH, ext)
	for _, a := range r.Assets {
		if a.Name == want {
			return a.Name, a.URL
		}
	}
	return "", ""
}

func unpackAsset(ctx context.Context, rel *release, repo, dest string) (ok bool, warnings []string, err error) {
	name, url := rel.asset(repo)
	if name == "" {
		return false, nil, nil
	}
	data, status, err := apiGet(ctx, url)
	if err != nil {
		return false, nil, err
	}
	if status != http.StatusOK {
		return false, nil, fmt.Errorf("downloading %s: status %d", name, status)
	}
	sumsURL := ""
	for _, a := range rel.Assets {
		if a.Name == "checksums.txt" {
			sumsURL = a.URL
		}
	}
	if sumsURL == "" {
		warnings = append(warnings, "the release has no checksums.txt: the download was not verified")
	} else {
		sums, status, err := apiGet(ctx, sumsURL)
		if err != nil || status != http.StatusOK {
			return false, nil, fmt.Errorf("downloading checksums.txt: status %d %v", status, err)
		}
		if err := verifyChecksum(sums, name, data); err != nil {
			return false, nil, err
		}
	}
	if err := extractArchive(data, name, dest); err != nil {
		return false, nil, err
	}
	return true, warnings, nil
}

func verifyChecksum(sums []byte, name string, data []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if !strings.EqualFold(f[0], got) {
				return fmt.Errorf("checksum of %s does not match: expected %s, got %s", name, f[0], got)
			}
			return nil
		}
	}
	return fmt.Errorf("checksums.txt does not list %s", name)
}

func gitClone(ctx context.Context, owner, repo, ref, dest string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("the repository has no release for this platform and git is not installed to clone it")
	}
	args := []string{"clone", "--depth", "1", "--quiet"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, fmt.Sprintf("%s/%s/%s.git", cloneBase, owner, repo), filepath.Join(dest, repo))
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		// without a terminal, git cannot ask for the credentials GitHub wants for a repository it does not show
		if strings.Contains(msg, "could not read Username") || strings.Contains(msg, "unable to get password") || strings.Contains(msg, "not found") {
			return fmt.Errorf("the repository %s/%s does not exist or is private", owner, repo)
		}
		return fmt.Errorf("git clone %s/%s: %s", owner, repo, msg)
	}
	os.RemoveAll(filepath.Join(dest, repo, ".git"))
	return nil
}

func safeJoin(dest, name string) (string, error) {
	// "/x" has no drive on Windows and would land inside dest, but an archive has no business naming absolute paths
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("the archive has an entry outside its folder: %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("the archive has an entry outside its folder: %q", name)
	}
	return filepath.Join(dest, clean), nil
}

func extractArchive(data []byte, name, dest string) error {
	var total int64
	write := func(rel string, mode fs.FileMode, r io.Reader) error {
		target, err := safeJoin(dest, rel)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()|0o600)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(r, maxUnpacked-total+1))
		f.Close()
		total += n
		if err == nil && total > maxUnpacked {
			err = errors.New("the archive is too large once unpacked")
		}
		return err
	}
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if !f.Mode().IsRegular() {
				return fmt.Errorf("the archive has %q, which is not a regular file", f.Name)
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			err = write(f.Name, f.Mode(), rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg:
			if err := write(h.Name, fs.FileMode(h.Mode), tr); err != nil {
				return err
			}
		case tar.TypeXGlobalHeader:
		default:
			return fmt.Errorf("the archive has %q, which is not a regular file", h.Name)
		}
	}
}
