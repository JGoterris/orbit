package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/JGoterris/orbit"
)

var (
	schemaOnce sync.Once
	schemaDoc  *Map
)

// schema is orbit.schema.json as an ordered mapping (the order of "properties" decides which suggestion wins a tie).
func schema() *Map {
	schemaOnce.Do(func() {
		v, err := ParseYAML(string(orbit.Schema)) // JSON is YAML
		if err != nil {
			panic("orbit.schema.json: " + err.Error())
		}
		schemaDoc = v.(*Map)
	})
	return schemaDoc
}

// UnknownKey is a key the schema does not allow.
type UnknownKey struct {
	// Path is the dotted path of the mapping that holds the key, e.g. `services.api`
	Path       string
	Key        string
	Suggestion string
}

func resolveRef(s *Map, root *Map) *Map {
	for {
		ref, ok := s.Get("$ref").(string)
		if !ok {
			return s
		}
		var node any = root
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			m, ok := node.(*Map)
			if !ok {
				node = nil
				break
			}
			node = m.Get(part)
		}
		next, ok := node.(*Map)
		if !ok {
			panic("unresolved $ref " + ref)
		}
		s = next
	}
}

func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	row := make([]int, len(rb)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		prev := row[0]
		row[0] = i
		for j := 1; j <= len(rb); j++ {
			tmp := row[j]
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			row[j] = min(row[j]+1, row[j-1]+1, prev+cost)
			prev = tmp
		}
	}
	return row[len(rb)]
}

// Suggest is the known key closest to key, or "" when none is close enough.
func Suggest(key string, known []string) string {
	best, bestD := "", 3
	for _, k := range known {
		if d := distance(strings.ToLower(key), strings.ToLower(k)); d < bestD {
			best, bestD = k, d
		}
	}
	return best
}

func schemaList(s *Map, key string) []*Map {
	list, _ := s.Get(key).([]any)
	var out []*Map
	for _, e := range list {
		if m, ok := e.(*Map); ok {
			out = append(out, m)
		}
	}
	return out
}

// UnknownKeys lists the keys of doc that the schema does not allow (only where it says `additionalProperties: false`).
func UnknownKeys(doc any) []UnknownKey {
	root := schema()
	var out []UnknownKey
	var walk func(value any, s *Map, path string)
	walk = func(value any, s *Map, path string) {
		s = resolveRef(s, root)
		branches := append(schemaList(s, "oneOf"), schemaList(s, "anyOf")...)
		if len(branches) > 0 {
			// follow the branch that describes this kind of value (mapping or list); scalars have no keys to check
			kind := ""
			if _, ok := value.([]any); ok {
				kind = "array"
			} else if isMapping(value) {
				kind = "object"
			}
			if kind != "" {
				for _, b := range branches {
					b = resolveRef(b, root)
					if t, _ := b.Get("type").(string); t == kind {
						walk(value, b, path)
					}
				}
			}
			return
		}
		if list, ok := value.([]any); ok {
			if items, ok := s.Get("items").(*Map); ok {
				for i, v := range list {
					walk(v, items, fmt.Sprintf("%s[%d]", path, i))
				}
			}
			return
		}
		m, ok := value.(*Map)
		if !ok {
			return
		}
		props, _ := s.Get("properties").(*Map)
		for _, key := range m.Keys() {
			at := key
			if path != "" {
				at = path + "." + key
			}
			child := m.Get(key)
			if p, ok := props.Get(key).(*Map); ok {
				walk(child, p, at)
				continue
			}
			switch ap := s.Get("additionalProperties").(type) {
			case *Map:
				walk(child, ap, at)
			case bool:
				if !ap {
					out = append(out, UnknownKey{Path: path, Key: key, Suggestion: Suggest(key, props.Keys())})
				}
			}
		}
	}
	walk(doc, root, "")
	return out
}

// Validation is the outcome of `orbit validate`.
type Validation struct {
	File string
	// LocalFile is the orbit.local.yaml next to File, when there is one
	LocalFile string
	Config    *OrbitConfig
	Errors    []string
}

// ValidateFile loads the file with the real loader (which decides what is valid) and, on top of that, reports the
// keys it would silently ignore. Loader errors are returned in Errors.
func ValidateFile(opts LoadOptions) Validation {
	v := Validation{}
	cfg, err := LoadConfig(opts)
	if err != nil {
		v.Errors = append(v.Errors, err.Error())
	}
	v.Config = cfg
	if cfg != nil {
		// a profile that is not active is not evaluated by the load above: try each one on its own
		for _, p := range cfg.AvailableProfiles {
			alone := opts
			alone.Profiles, alone.ProfilesSet = []string{p.Name}, true
			if _, perr := LoadConfig(alone); perr != nil {
				v.Errors = append(v.Errors, fmt.Sprintf("profile %s: %v", p.Name, perr))
			}
		}
	}
	switch {
	case cfg != nil && cfg.File != "":
		v.File = cfg.File
	case opts.File != "":
		v.File, _ = filepath.Abs(opts.File)
	default:
		dir := opts.Dir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		v.File = FindConfigFile(dir)
	}
	check := func(file, prefix string) {
		var doc any
		if data, rerr := os.ReadFile(file); rerr == nil {
			doc, _ = ParseYAML(string(data)) // the loader already reported invalid YAML
		}
		for _, u := range UnknownKeys(doc) {
			at := u.Key
			if u.Path != "" {
				at = u.Path + "." + u.Key
			}
			msg := prefix + at + ": unknown key"
			if u.Suggestion != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", u.Suggestion)
			}
			v.Errors = append(v.Errors, msg)
		}
	}
	if v.File != "" {
		check(v.File, "")
		if local := FindLocalConfigFile(filepath.Dir(v.File)); local != "" {
			v.LocalFile = local
			check(local, filepath.Base(local)+": ")
		}
	}
	return v
}
