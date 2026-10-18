package config

import (
	"os"
	"path/filepath"
	"strings"
)

// LocalConfigFilenames are the per-developer overrides of orbit.yaml, looked up next to it.
var LocalConfigFilenames = []string{"orbit.local.yaml", "orbit.local.yml"}

// FindLocalConfigFile is the override file in root; "" when there is none.
func FindLocalConfigFile(root string) string {
	for _, name := range LocalConfigFilenames {
		if p := filepath.Join(root, name); exists(p) {
			return p
		}
	}
	return ""
}

// readYAMLDoc reads a config file as a mapping. Errors are reported with the file as their path.
func readYAMLDoc(file string) *Map {
	text, err := os.ReadFile(file)
	if err != nil {
		fail(err.Error(), file)
	}
	parsed, err := ParseYAML(string(text))
	if err != nil {
		msg := err.Error()
		hint := ""
		if l := strings.ToLower(msg); strings.Contains(l, "alias") || strings.Contains(l, "anchor") {
			hint = ` (quote glob patterns that start with *, e.g. "**/*.py")`
		}
		fail("invalid YAML: "+msg+hint, file)
	}
	return asRecord(parsed, file)
}

// mergeDoc lays over on top of base: mappings merge key by key, anything else (scalars, lists) replaces, and a
// null removes the key. Neither argument is modified; base may be nil.
func mergeDoc(base, over *Map) *Map {
	out := NewMap()
	for _, k := range base.Keys() {
		out.Set(k, base.Get(k))
	}
	for _, k := range over.Keys() {
		v := over.Get(k)
		if v == nil {
			out.Delete(k)
			continue
		}
		if om, ok := v.(*Map); ok {
			// merged even where base has nothing, so the nulls inside do not survive as keys
			bm, _ := base.Get(k).(*Map)
			out.Set(k, mergeDoc(bm, om))
			continue
		}
		out.Set(k, v)
	}
	return out
}
