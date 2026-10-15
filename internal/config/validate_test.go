package config

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func yamlDoc(t *testing.T, text string) any {
	t.Helper()
	v, err := ParseYAML(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSchemaRefsResolve(t *testing.T) {
	root := schema()
	refs := regexp.MustCompile(`"\$ref":\s*"#/definitions/([A-Za-z]+)"`).FindAllStringSubmatch(jsonOf(root), -1)
	if len(refs) < 10 {
		t.Fatalf("only %d refs found", len(refs))
	}
	defs := root.Get("definitions").(*Map)
	for _, r := range refs {
		if !defs.Has(r[1]) {
			t.Errorf("unresolved $ref %s", r[1])
		}
	}
}

// schemaKeys is every property name the schema defines, at any depth.
func schemaKeys(node any, out map[string]bool) {
	switch x := node.(type) {
	case []any:
		for _, n := range x {
			schemaKeys(n, out)
		}
	case *Map:
		for _, k := range x.Keys() {
			if k == "properties" {
				if p, ok := x.Get(k).(*Map); ok {
					for _, pk := range p.Keys() {
						out[pk] = true
					}
				}
			}
			schemaKeys(x.Get(k), out)
		}
	}
}

func TestSchemaCoversEveryKeyTheLoaderReads(t *testing.T) {
	known := map[string]bool{}
	schemaKeys(schema(), known)
	var src strings.Builder
	for _, f := range []string{"load.go", "envfiles.go"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(data)
	}
	call := regexp.MustCompile(`\b(?:rec|doc)\.(?:Get|Has|First)\(([^)]*)\)`)
	quoted := regexp.MustCompile(`"([A-Za-z_]+)"`)
	read := map[string]bool{}
	for _, m := range call.FindAllStringSubmatch(src.String(), -1) {
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			read[q[1]] = true
		}
	}
	if len(read) < 30 {
		t.Fatalf("only %d keys found in the loader", len(read))
	}
	for k := range read {
		if !known[k] {
			t.Errorf("the loader reads %q but the schema does not know it", k)
		}
	}
}

func TestDocsExampleHasNoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```yaml\\r?\\n(name: my-stack.*?)```").FindSubmatch(data)
	if m == nil {
		t.Fatal("docs/configuration.md example not found")
	}
	if got := UnknownKeys(yamlDoc(t, string(m[1]))); len(got) != 0 {
		t.Fatalf("unknown keys in the docs/configuration.md example: %+v", got)
	}
}

func TestFixtureStackIsValid(t *testing.T) {
	res := ValidateFile(LoadOptions{Dir: filepath.Join("..", "..", "test", "fixtures", "stack"), Env: noEnv})
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
}

func TestTyposAreReportedWithSuggestion(t *testing.T) {
	doc := yamlDoc(t, `
nmae: x
services:
  api:
    cmd: run
    helth: "tcp:1"
    depend_on: [db]
    watch: { paths: [a], debonce: 1s }
    health: { http: "http://x", intervall: 1s }
    pre_start: [{ cmd: x, timout: 1s }]
    ready_when: { logg: x }
groups: { g: [api] }
`)
	want := []UnknownKey{
		{Path: "", Key: "nmae", Suggestion: "name"},
		{Path: "services.api", Key: "helth", Suggestion: "health"},
		{Path: "services.api", Key: "depend_on", Suggestion: "depends_on"},
		{Path: "services.api.watch", Key: "debonce", Suggestion: "debounce"},
		{Path: "services.api.health", Key: "intervall", Suggestion: "interval"},
		{Path: "services.api.pre_start[0]", Key: "timout", Suggestion: "timeout"},
		{Path: "services.api.ready_when", Key: "logg", Suggestion: "log"},
	}
	if got := UnknownKeys(doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown keys = %+v\nwant %+v", got, want)
	}
}

func TestValidFormsAreAccepted(t *testing.T) {
	doc := yamlDoc(t, `
compose: false
env: { A: 1, B: x }
services:
  a: bun run dev
  b: { command: x, environment: [K=v], dependsOn: a, env_file: [.env, { path: .x, required: false }], health: false, watch: ["src/**"] }
  c: { image: redis, ports: ["1:1", 2], volumes: [./d:/d] }
  d: { type: external, health: "tcp:db:5432" }
  e:
`)
	if got := UnknownKeys(doc); len(got) != 0 {
		t.Fatalf("unexpected unknown keys: %+v", got)
	}
}

func orbitYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidateFileReturnsLoaderErrors(t *testing.T) {
	res := ValidateFile(LoadOptions{Dir: orbitYAML(t, "services:\n  a: { cmd: x, restart: sometimes }\n"), Env: noEnv})
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "restart") {
		t.Fatalf("errors = %v", res.Errors)
	}
}

func TestValidateFileReportsUnknownKeys(t *testing.T) {
	res := ValidateFile(LoadOptions{Dir: orbitYAML(t, "services:\n  a: { cmd: x, helth: 'tcp:1' }\n"), Env: noEnv})
	if res.Config == nil {
		t.Fatal("the config should still load")
	}
	want := []string{`services.a.helth: unknown key (did you mean "health"?)`}
	if !reflect.DeepEqual(res.Errors, want) {
		t.Fatalf("errors = %v, want %v", res.Errors, want)
	}
}

func TestValidFileHasNoErrors(t *testing.T) {
	res := ValidateFile(LoadOptions{Dir: orbitYAML(t, "services:\n  a: { cmd: x, port: 3000 }\ngroups: { all: [a] }\n"), Env: noEnv})
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v", res.Errors)
	}
	if !reflect.DeepEqual(res.Config.Order, []string{"a"}) {
		t.Fatalf("services = %v", res.Config.Order)
	}
}
