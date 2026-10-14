package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// parseEnvFileRefs reads `env_file: .env` | `[.env, .env.local]` | `[{ path: .env.local, required: false }]`.
func parseEnvFileRefs(raw any, path, root string) []EnvFileRef {
	if raw == nil {
		return nil
	}
	items, isList := raw.([]any)
	if !isList {
		items = []any{raw}
	}
	out := make([]EnvFileRef, 0, len(items))
	for i, item := range items {
		p := path
		if isList {
			p = fmt.Sprintf("%s[%d]", path, i)
		}
		if s, ok := item.(string); ok {
			out = append(out, EnvFileRef{Path: resolvePath(s, root), Required: true})
			continue
		}
		rec := asRecord(item, p)
		file, _ := asString(rec.Get("path"), p+".path")
		if file == "" {
			fail("env_file entry needs `path`", p)
		}
		out = append(out, EnvFileRef{Path: resolvePath(file, root), Required: rec.Get("required") != false})
	}
	return out
}

func resolvePath(file, root string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(root, file)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MissingEnvFiles lists the required files that do not exist.
func MissingEnvFiles(refs []EnvFileRef) []string {
	var out []string
	for _, r := range refs {
		if r.Required && !exists(r.Path) {
			out = append(out, r.Path)
		}
	}
	return out
}

// ReadEnvFiles reads the files in order; later files override earlier ones. It fails if a required file is missing.
func ReadEnvFiles(refs []EnvFileRef) (map[string]string, error) {
	if missing := MissingEnvFiles(refs); len(missing) > 0 {
		return nil, fmt.Errorf("env_file not found: %s", strings.Join(missing, ", "))
	}
	out := map[string]string{}
	for _, r := range refs {
		data, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		for k, v := range ParseDotEnv(string(data)) {
			out[k] = v
		}
	}
	return out, nil
}

type EnvEntry struct {
	Key   string
	Value string
	// Source is the .env file it came from (relative to root), or "inline"
	Source string
}

func relOr(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil && r != "" {
		return r
	}
	return path
}

// ResolveEnv is the same merge as a service start (inline over files), but never fails: missing required files are
// reported instead. Entries are sorted by key.
func ResolveEnv(env map[string]string, refs []EnvFileRef, root string) (entries []EnvEntry, missing []string) {
	merged := map[string]EnvEntry{}
	for _, r := range refs {
		data, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		source := relOr(root, r.Path)
		for k, v := range ParseDotEnv(string(data)) {
			merged[k] = EnvEntry{Key: k, Value: v, Source: source}
		}
	}
	for k, v := range env {
		merged[k] = EnvEntry{Key: k, Value: v, Source: "inline"}
	}
	for _, e := range merged {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	for _, f := range MissingEnvFiles(refs) {
		missing = append(missing, relOr(root, f))
	}
	return entries, missing
}
