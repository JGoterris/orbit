package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/userconfig"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

const metaFile = ".orbit-plugin.json"

// Meta is what orbit remembers about where an installed plugin came from.
type Meta struct {
	// Source is "owner/repo" for a GitHub plugin, or the folder it was copied or linked from
	Source  string `json:"source"`
	Version string `json:"version"`
	// Ref is the tag the user asked for; an upgrade stays on it
	Ref string `json:"ref,omitempty"`
	// Linked: Dir is the folder itself (a plugin under development), not a copy
	Linked bool `json:"linked,omitempty"`
	// Path is the folder of a linked plugin
	Path string `json:"path,omitempty"`
}

type Plugin struct {
	Dir      string
	Manifest *protocol.Manifest
	Meta     Meta
}

func Root() string { return filepath.Join(platform.DataBase(), "orbit", "plugins") }

func ConfigDir(name string) string { return filepath.Join(userconfig.Dir(), "plugins", name) }

func StateDir(name string) string {
	return filepath.Join(platform.StateBase(), "orbit", "plugins", name)
}

func LogFile(name string) string { return filepath.Join(StateDir(name), "plugin.log") }

// Installed lists the installed plugins sorted by name. A folder whose manifest does not load is returned in the
// second value, so `orbit plugin ls` can show why a plugin is missing instead of hiding it.
func Installed() ([]*Plugin, map[string]error) {
	entries, _ := os.ReadDir(Root())
	var out []*Plugin
	broken := map[string]error{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, err := Load(e.Name())
		if err != nil {
			broken[e.Name()] = err
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out, broken
}

func Load(name string) (*Plugin, error) {
	dir := filepath.Join(Root(), name)
	meta, _ := readMeta(dir)
	real := dir
	if meta.Linked && meta.Path != "" {
		real = meta.Path
	}
	m, err := ReadManifest(real)
	if err != nil {
		return nil, err
	}
	if m.Name != name {
		return nil, fmt.Errorf("the folder is %q but the manifest says %q", name, m.Name)
	}
	return &Plugin{Dir: real, Manifest: m, Meta: meta}, nil
}

func readMeta(dir string) (Meta, error) {
	var m Meta
	data, err := os.ReadFile(filepath.Join(dir, metaFile))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(data, &m)
}

func writeMeta(dir string, m Meta) error {
	if !userconfig.WriteJSONAtomic(filepath.Join(dir, metaFile), m) {
		return errors.New("cannot write " + metaFile)
	}
	return nil
}

// Remove deletes an installed plugin (for a linked one, only the link: the source folder stays).
func Remove(name string) error {
	dir := filepath.Join(Root(), name)
	if _, err := os.Lstat(dir); err != nil {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	return os.RemoveAll(dir)
}
