package plugins

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/JGoterris/orbit"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

func yamlKeys(t reflect.Type, seen map[string]bool, path string) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}
		seen[key] = true
		yamlKeys(f.Type, seen, path+"."+key)
	}
}

// TestSchemaCoversTheManifest keeps orbit-plugin.schema.json from drifting from the types the manifest is read into.
func TestSchemaCoversTheManifest(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(orbit.PluginSchema, &schema); err != nil {
		t.Fatalf("the schema is not JSON: %v", err)
	}
	text := string(orbit.PluginSchema)
	keys := map[string]bool{}
	yamlKeys(reflect.TypeOf(protocol.Manifest{}), keys, "")
	for k := range keys {
		if !strings.Contains(text, `"`+k+`"`) {
			t.Errorf("the schema does not know the key %q of the manifest", k)
		}
	}
}

func TestSchemaKnowsEveryKeyOfAFullManifest(t *testing.T) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(goodManifest), &doc); err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	json.Unmarshal(orbit.PluginSchema, &schema)
	var walk func(v any, s map[string]any, path string)
	resolve := func(s map[string]any) map[string]any {
		if ref, ok := s["$ref"].(string); ok {
			return schema["definitions"].(map[string]any)[strings.TrimPrefix(ref, "#/definitions/")].(map[string]any)
		}
		return s
	}
	walk = func(v any, s map[string]any, path string) {
		s = resolve(s)
		switch x := v.(type) {
		case map[string]any:
			props, _ := s["properties"].(map[string]any)
			for k, child := range x {
				cs, ok := props[k].(map[string]any)
				if !ok {
					t.Errorf("%s.%s is accepted by the manifest but not by the schema", path, k)
					continue
				}
				walk(child, cs, path+"."+k)
			}
		case []any:
			items, _ := s["items"].(map[string]any)
			for _, e := range x {
				if items != nil {
					walk(e, items, path+"[]")
				}
			}
		}
	}
	walk(doc, schema, "")
}
