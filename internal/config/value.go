package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Map is a YAML mapping that remembers the order of its keys, like a JS object does.
// The other YAML values are nil, bool, int64, float64, string and []any.
type Map struct {
	keys []string
	vals map[string]any
}

func NewMap() *Map { return &Map{vals: map[string]any{}} }

// Set adds or replaces a key; a new key goes last.
func (m *Map) Set(key string, v any) {
	if _, ok := m.vals[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.vals[key] = v
}

// Lookup distinguishes an absent key from one set to null.
func (m *Map) Lookup(key string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m.vals[key]
	return v, ok
}

// Get is nil when the key is absent or null.
func (m *Map) Get(key string) any {
	v, _ := m.Lookup(key)
	return v
}

// Has reports whether the key is present, even with a null value (`rec.x !== undefined` in JS).
func (m *Map) Has(key string) bool {
	_, ok := m.Lookup(key)
	return ok
}

// First is the first of the keys whose value is not null (`rec.a ?? rec.b`).
func (m *Map) First(keys ...string) any {
	for _, k := range keys {
		if v := m.Get(k); v != nil {
			return v
		}
	}
	return nil
}

func (m *Map) Keys() []string {
	if m == nil {
		return nil
	}
	return m.keys
}

func (m *Map) Len() int { return len(m.Keys()) }

func (m *Map) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range m.Keys() {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// ParseYAML reads a YAML document into nil, bool, int64, float64, string, []any and *Map. Aliases are resolved and
// `<<` merge keys applied (explicit keys win). An empty document is nil.
func ParseYAML(text string) (any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, nil
	}
	return fromNode(doc.Content[0], 0)
}

func fromNode(n *yaml.Node, depth int) (any, error) {
	if depth > 200 {
		return nil, fmt.Errorf("document is nested too deeply (or an alias refers to itself)")
	}
	switch n.Kind {
	case yaml.AliasNode:
		return fromNode(n.Alias, depth+1)
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := fromNode(c, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.MappingNode:
		m := NewMap()
		var merges []*yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
				merges = append(merges, v)
				continue
			}
			key, err := keyString(k, depth)
			if err != nil {
				return nil, err
			}
			val, err := fromNode(v, depth+1)
			if err != nil {
				return nil, err
			}
			m.Set(key, val)
		}
		for _, src := range merges {
			sources := []*yaml.Node{src}
			if r := resolveAlias(src); r.Kind == yaml.SequenceNode {
				sources = r.Content
			}
			for _, s := range sources {
				v, err := fromNode(s, depth+1)
				if err != nil {
					return nil, err
				}
				sm, ok := v.(*Map)
				if !ok {
					return nil, fmt.Errorf("line %d: map merge requires a map or a sequence of maps", s.Line)
				}
				for _, k := range sm.keys {
					if !m.Has(k) {
						m.Set(k, sm.vals[k])
					}
				}
			}
		}
		return m, nil
	case yaml.ScalarNode:
		return scalar(n)
	}
	return nil, nil
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

func keyString(k *yaml.Node, depth int) (string, error) {
	k = resolveAlias(k)
	if k.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("line %d: only scalar mapping keys are supported", k.Line)
	}
	return k.Value, nil
}

func scalar(n *yaml.Node) (any, error) {
	switch n.ShortTag() {
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return n.Value, nil
		}
		return b, nil
	case "!!int":
		var i int64
		if err := n.Decode(&i); err == nil {
			return i, nil
		}
		var f float64
		if err := n.Decode(&f); err == nil {
			return f, nil
		}
		return n.Value, nil
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return n.Value, nil
		}
		return f, nil
	}
	return n.Value, nil
}

// ------------------------------------------------------------------ errors

// ConfigError is a mistake in orbit.yaml (or a compose file), with the path of the offending key when known.
type ConfigError struct {
	Msg  string
	Path string
}

func (e *ConfigError) Error() string {
	if e.Path != "" {
		return e.Path + ": " + e.Msg
	}
	return e.Msg
}

// The parsers below are deeply nested and report the first problem they find, so they signal it with a panic that
// carries a *ConfigError; every exported entry point turns it back into an ordinary error.
func fail(msg, path string) { panic(&ConfigError{Msg: msg, Path: path}) }

func recoverConfig(err *error) {
	if r := recover(); r != nil {
		if ce, ok := r.(*ConfigError); ok {
			*err = ce
			return
		}
		panic(r)
	}
}

// ------------------------------------------------------------------ typed access

func jsString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case int64, float64:
		return "number"
	}
	return "object"
}

func jsonOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// asString accepts strings, numbers and booleans; ok is false for an absent or null value.
func asString(v any, path string) (string, bool) {
	switch v.(type) {
	case nil:
		return "", false
	case string, bool, int64, float64:
		return jsString(v), true
	}
	fail(fmt.Sprintf("expected a string, got %s", typeName(v)), path)
	return "", false
}

func strOr(v any, path, fallback string) string {
	if s, ok := asString(v, path); ok {
		return s
	}
	return fallback
}

func asStringList(v any, path string) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for i, e := range x {
			s, _ := asString(e, fmt.Sprintf("%s[%d]", path, i))
			out = append(out, s)
		}
		return out
	}
	fail("expected a list", path)
	return nil
}

func asRecord(v any, path string) *Map {
	switch x := v.(type) {
	case nil:
		return NewMap()
	case *Map:
		return x
	}
	fail("expected a mapping", path)
	return nil
}

func isMapping(v any) bool { _, ok := v.(*Map); return ok }

// asEnv accepts `{ KEY: value }` or `["KEY=value"]` (a bare `KEY` takes the value from orbit's own environment).
func asEnv(v any, path string) map[string]string {
	out := map[string]string{}
	if list, ok := v.([]any); ok {
		for i, e := range list {
			s, _ := asString(e, fmt.Sprintf("%s[%d]", path, i))
			if k, val, found := strings.Cut(s, "="); found {
				out[k] = val
			} else {
				out[s] = os.Getenv(s)
			}
		}
		return out
	}
	rec := asRecord(v, path)
	for _, k := range rec.Keys() {
		out[k] = strOr(rec.Get(k), path+"."+k, "")
	}
	return out
}

// truthy follows JS: null, false, 0, NaN and "" are falsy.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int64:
		return x != 0
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

// toNumber is JS Number(v) for the values a YAML document can hold.
func toNumber(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case int64:
		return float64(x)
	case float64:
		return x
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	return math.NaN()
}

func environ() map[string]string {
	out := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
			out[k] = v
		}
	}
	return out
}
