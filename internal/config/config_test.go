package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var noEnv = map[string]string{}

func mustLoad(t *testing.T, files map[string]string) *OrbitConfig {
	t.Helper()
	cfg, err := LoadConfig(LoadOptions{Dir: project(t, files), Env: noEnv})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func loadErr(t *testing.T, files map[string]string) string {
	t.Helper()
	_, err := LoadConfig(LoadOptions{Dir: project(t, files), Env: noEnv})
	if err == nil {
		t.Fatal("expected an error")
	}
	return err.Error()
}

func wantContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("error %q does not contain %q", got, want)
	}
}

func TestEmptyDirectory(t *testing.T) {
	dir := project(t, nil)
	_, err := LoadConfig(LoadOptions{Dir: dir})
	if err == nil {
		t.Fatal("expected an error")
	}
	wantContains(t, err.Error(), "no orbit.yaml or docker-compose.yml")
	cfg, err := LoadConfig(LoadOptions{Dir: dir, AllowEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Services) != 0 || cfg.Root != dir || cfg.Name != filepath.Base(dir) {
		t.Fatalf("empty config = %+v", cfg)
	}
}

func TestDurationsAndPorts(t *testing.T) {
	for in, want := range map[string]int64{"2s": 2000, "150ms": 150, "1m": 60_000, "1.5s": 1500, "7": 7} {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if _, err := ParseDuration("soon"); err == nil {
		t.Error("ParseDuration(soon) should fail")
	}
	for in, want := range map[string]int{"8080:80": 8080, "127.0.0.1:5432:5432/tcp": 5432, "3000": 3000, "x:y": 0} {
		if got := HostPortOf(in); got != want {
			t.Errorf("HostPortOf(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestMemSize(t *testing.T) {
	for in, want := range map[string]int64{"1G": 1 << 30, "512m": 512 << 20, "1.5gb": 3 << 29, "100": 100} {
		got, ok := ParseMemSize(in)
		if !ok || got != want {
			t.Errorf("ParseMemSize(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "lots", "0", "1x"} {
		if _, ok := ParseMemSize(in); ok {
			t.Errorf("ParseMemSize(%q) should be invalid", in)
		}
	}
}

func TestLifecycleHooks(t *testing.T) {
	cfg := mustLoad(t, map[string]string{"orbit.yaml": `services:
  api:
    cmd: sleep 1
    pre_start: rm -rf tmp/*
    post_start:
      - ./check.sh
      - { cmd: curl localhost, timeout: 10s }
    post_stop: { cmd: echo bye }
  plain:
    cmd: sleep 1
`})
	want := &Hooks{
		PreStart:  []Hook{{Cmd: "rm -rf tmp/*", Timeout: 60_000}},
		PostStart: []Hook{{Cmd: "./check.sh", Timeout: 60_000}, {Cmd: "curl localhost", Timeout: 10_000}},
		PostStop:  []Hook{{Cmd: "echo bye", Timeout: 60_000}},
	}
	if got := cfg.Services["api"].Hooks; !reflect.DeepEqual(got, want) {
		t.Errorf("hooks = %+v, want %+v", got, want)
	}
	if cfg.Services["plain"].Hooks != nil {
		t.Error("a service without hooks must have none")
	}
}

func TestInvalidHooks(t *testing.T) {
	svc := func(body string) map[string]string {
		return map[string]string{"orbit.yaml": "services:\n  a:\n    cmd: sleep 1\n" + body}
	}
	wantContains(t, loadErr(t, svc("    pre_start: ''\n")), "non-empty `cmd`")
	wantContains(t, loadErr(t, svc("    pre_start: { timeout: 1s }\n")), "non-empty `cmd`")
	wantContains(t, loadErr(t, svc("    oneshot: true\n    post_start: echo hi\n")), "oneshot")
	cfg := mustLoad(t, svc("    oneshot: true\n    post_stop: echo hi\n"))
	if n := len(cfg.Services["a"].Hooks.PostStop); n != 1 {
		t.Errorf("post_stop hooks = %d, want 1", n)
	}
}

func TestReadyWhen(t *testing.T) {
	svc := func(body string) map[string]string {
		return map[string]string{"orbit.yaml": "services:\n  a:\n    cmd: sleep 1\n" + body}
	}
	got := func(body string) *ReadyWhen { return mustLoad(t, svc(body)).Services["a"].ReadyWhen }
	if rw := got("    ready_when: { log: Listening on }\n"); rw == nil || rw.Log != "Listening on" {
		t.Errorf("plain pattern = %+v", rw)
	}
	if rw := got("    ready_when: { log: '/port \\d+/i' }\n"); rw == nil || rw.Log != `/port \d+/i` {
		t.Errorf("regex pattern = %+v", rw)
	}
	if got("") != nil {
		t.Error("no ready_when must be nil")
	}
	wantContains(t, loadErr(t, svc("    ready_when: { log: '/(/' }\n")), "invalid regex")
	wantContains(t, loadErr(t, svc("    ready_when: {}\n")), "non-empty `log`")
	wantContains(t, loadErr(t, svc("    oneshot: true\n    ready_when: { log: x }\n")), "oneshot")
	wantContains(t, loadErr(t, map[string]string{"orbit.yaml": "services:\n  e: { type: external, health: 'tcp:1', ready_when: { log: x } }"}), "has no effect")
}

func TestLogPattern(t *testing.T) {
	match, err := CompileLogPattern(`/port (\d+)/i`)
	if err != nil {
		t.Fatal(err)
	}
	if !match("Listening on PORT 8080") || match("nothing here") {
		t.Error("regex pattern did not match as expected")
	}
	sub, _ := CompileLogPattern("Listening on")
	if !sub("x Listening on :80") || sub("listening on") {
		t.Error("substring pattern did not match as expected")
	}
}

func TestExternalServices(t *testing.T) {
	ext := func(body string) map[string]string {
		return map[string]string{"orbit.yaml": "services:\n  ext:\n    type: external\n" + body}
	}
	c := mustLoad(t, ext("    health: \"https://x.test/up\"\n    url: https://x.test\n    console: psql \"$DB\""))
	svc := c.Services["ext"]
	if svc.Type != TypeExternal || svc.URL != "https://x.test" || svc.Health == nil || svc.Health.Http != "https://x.test/up" {
		t.Errorf("external service = %+v", svc)
	}
	if h := mustLoad(t, ext("    health: \"tcp:db.test:5432\"")).Services["ext"].Health; h == nil || h.Tcp != "db.test:5432" {
		t.Errorf("tcp health = %+v", h)
	}
	wantContains(t, loadErr(t, ext("    port: 5432")), "needs `health`")
	for _, key := range []string{"cmd: x", "watch: ['a']", "pre_start: x", "oneshot: true"} {
		wantContains(t, loadErr(t, ext("    health: \"tcp:1\"\n    "+key)), "has no effect")
	}
	dep := mustLoad(t, map[string]string{"orbit.yaml": "services:\n  a: { cmd: x, depends_on: [ext] }\n  ext: { type: external, health: 'tcp:1' }"})
	if !reflect.DeepEqual(dep.Services["a"].DependsOn, []string{"ext"}) {
		t.Errorf("depends_on = %v", dep.Services["a"].DependsOn)
	}
	wantContains(t, loadErr(t, map[string]string{"orbit.yaml": "services:\n  ext: { type: external, health: 'tcp:1', depends_on: [a] }\n  a: { cmd: x }"}), "has no effect")
}

func TestEnvInterpolationAndDotEnv(t *testing.T) {
	got := ParseDotEnv("A=1\n# c\nexport B='two'\nC=3 # note")
	if want := (map[string]string{"A": "1", "B": "two", "C": "3"}); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseDotEnv = %v, want %v", got, want)
	}
	m := NewMap()
	m.Set("a", "${X}-${Y:-def}")
	out := Interpolate(m, map[string]string{"X": "x"}).(*Map)
	if out.Get("a") != "x-def" {
		t.Errorf("interpolated = %v", out.Get("a"))
	}
}

func TestLoadsServicesInfersTypesAndMergesCompose(t *testing.T) {
	cfg, err := LoadConfig(LoadOptions{Env: noEnv, Dir: project(t, map[string]string{
		"docker-compose.yml": `
services:
  postgres:
    image: postgres:16
    ports: ["5433:5432"]
    healthcheck: { test: ["CMD", "pg_isready"] }
  mailer:
    image: mailhog/mailhog
    depends_on:
      postgres: { condition: service_healthy }
`,
		".env": "API_PORT=4000",
		"orbit.yaml": `
name: demo
services:
  api:
    cmd: bun run dev
    port: ${API_PORT}
    depends_on: [postgres, redis]
    restart: on-failure
  redis:
    image: redis:7
    ports: ["6379:6379"]
  postgres:
    restart: always
groups:
  backend: [api, postgres]
`,
	})})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "demo" {
		t.Errorf("name = %q", cfg.Name)
	}
	if want := []string{"mailer", "api", "redis", "postgres"}; !reflect.DeepEqual(cfg.Order, want) {
		t.Errorf("order = %v, want %v", cfg.Order, want)
	}
	s := cfg.Services
	if s["api"].Type != TypeProcess || s["api"].Port != 4000 || s["api"].Health == nil || s["api"].Health.Tcp != "4000" {
		t.Errorf("api = %+v", s["api"])
	}
	if s["redis"].Type != TypeDocker {
		t.Errorf("redis type = %s", s["redis"].Type)
	}
	pg := s["postgres"]
	if pg.Type != TypeCompose || pg.Restart != RestartAlways || pg.Port != 5433 || pg.Health == nil || !pg.Health.Container {
		t.Errorf("postgres = %+v", pg)
	}
	if !reflect.DeepEqual(s["mailer"].DependsOn, []string{"postgres"}) {
		t.Errorf("mailer depends_on = %v", s["mailer"].DependsOn)
	}
	if !reflect.DeepEqual(cfg.Groups["backend"], []string{"api", "postgres"}) {
		t.Errorf("groups = %v", cfg.Groups)
	}
}

func TestRejectsUnknownDepsAndCycles(t *testing.T) {
	wantContains(t, loadErr(t, map[string]string{"orbit.yaml": "services:\n  a: { cmd: x, depends_on: [b] }"}), `unknown service "b"`)
	wantContains(t, loadErr(t, map[string]string{"orbit.yaml": "services:\n  a: { cmd: x, depends_on: [b] }\n  b: { cmd: y, depends_on: [a] }"}), "cycle")
	wantContains(t, loadErr(t, map[string]string{"orbit.yaml": "services:\n  a: { port: 1 }"}), "needs `cmd`")
}

func TestComposeOnlyProject(t *testing.T) {
	c := mustLoad(t, map[string]string{"compose.yaml": "services:\n  web: { image: nginx, ports: ['8081:80'] }"})
	if c.Services["web"].Type != TypeCompose {
		t.Errorf("type = %s", c.Services["web"].Type)
	}
}

func TestComposeInterpolationDefaults(t *testing.T) {
	c := mustLoad(t, map[string]string{"compose.yaml": "services:\n  gw:\n    image: x\n    ports: ['${GW_PORT:-8280}:8280']"})
	if c.Services["gw"].Port != 8280 {
		t.Errorf("port = %d", c.Services["gw"].Port)
	}
}

func TestComposeMergeKeys(t *testing.T) {
	c := mustLoad(t, map[string]string{"compose.yaml": `
x-base: &base
  image: nginx
  ports: ["9000:80"]
services:
  a:
    <<: *base
  b:
    <<: *base
    ports: ["9100:80"]
`})
	if c.Services["a"].Port != 9000 || c.Services["b"].Port != 9100 || c.Services["a"].Image != "nginx" {
		t.Errorf("a = %+v, b = %+v", c.Services["a"], c.Services["b"])
	}
}

func TestEnvFiles(t *testing.T) {
	dir := project(t, map[string]string{
		"common.env": "A=common\nB=common",
		"api.env":    "B=api\nC=\"line1\\nline2\"",
		"orbit.yaml": `
env_file: common.env
env: { G: inline }
services:
  api:
    cmd: x
    env_file: [api.env, { path: missing.env, required: false }]
    env: { C: override }
  job:
    cmd: y
`,
	})
	c, err := LoadConfig(LoadOptions{Dir: dir, Env: noEnv})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range c.Services["api"].EnvFiles {
		paths = append(paths, f.Path)
	}
	want := []string{filepath.Join(dir, "common.env"), filepath.Join(dir, "api.env"), filepath.Join(dir, "missing.env")}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("api env files = %v, want %v", paths, want)
	}
	if n := len(c.Services["job"].EnvFiles); n != 1 {
		t.Errorf("job env files = %d, want 1", n)
	}
	env, err := ReadEnvFiles(c.Services["api"].EnvFiles)
	if err != nil {
		t.Fatal(err)
	}
	if w := (map[string]string{"A": "common", "B": "api", "C": "line1\nline2"}); !reflect.DeepEqual(env, w) {
		t.Errorf("env = %v, want %v", env, w)
	}
	if c.Services["api"].Env["G"] != "inline" || c.Services["api"].Env["C"] != "override" {
		t.Errorf("inline env = %v", c.Services["api"].Env)
	}
}

func TestMissingRequiredEnvFile(t *testing.T) {
	wantContains(t, loadErr(t, map[string]string{"orbit.yaml": "services:\n  api: { cmd: x, env_file: nope.env }"}), "env_file not found")
}

func TestResolveEnv(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.env"), []byte("A=1\nB=file\n"), 0o644)
	entries, missing := ResolveEnv(map[string]string{"B": "inline"}, []EnvFileRef{
		{Path: filepath.Join(dir, "a.env"), Required: true},
		{Path: filepath.Join(dir, "nope.env"), Required: true},
		{Path: filepath.Join(dir, "opt.env"), Required: false},
	}, dir)
	want := []EnvEntry{{Key: "A", Value: "1", Source: "a.env"}, {Key: "B", Value: "inline", Source: "inline"}}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}
	if !reflect.DeepEqual(missing, []string{"nope.env"}) {
		t.Errorf("missing = %v", missing)
	}
}

func TestInvalidYAMLHint(t *testing.T) {
	msg := loadErr(t, map[string]string{"orbit.yaml": "services: [oops"})
	wantContains(t, msg, "invalid YAML")
}

const localBase = `
services:
  api:
    cmd: run api
    port: 3000
    env: { A: "1", B: "2" }
    depends_on: [db, redis]
  db: { cmd: run db, port: 5432 }
  redis: { cmd: run redis, port: 6379 }
  worker: { cmd: run worker }
`

func TestLocalOverrideMergesAndReplaces(t *testing.T) {
	cfg := mustLoad(t, map[string]string{
		"orbit.yaml": localBase,
		"orbit.local.yaml": `
services:
  api:
    port: 4000
    env: { B: "9", C: "3" }
    depends_on: [db]
  extra: { cmd: run extra }
`,
	})
	api := cfg.Services["api"]
	if api.Port != 4000 || api.Cmd != "run api" {
		t.Fatalf("port/cmd = %d/%q", api.Port, api.Cmd)
	}
	if !reflect.DeepEqual(api.Env, map[string]string{"A": "1", "B": "9", "C": "3"}) {
		t.Fatalf("env = %v", api.Env)
	}
	if !reflect.DeepEqual(api.DependsOn, []string{"db"}) {
		t.Fatalf("depends_on = %v", api.DependsOn)
	}
	if cfg.Services["extra"] == nil || !strings.HasSuffix(cfg.LocalFile, "orbit.local.yaml") {
		t.Fatalf("extra = %v, local = %q", cfg.Services["extra"], cfg.LocalFile)
	}
}

func TestLocalNullRemoves(t *testing.T) {
	cfg := mustLoad(t, map[string]string{
		"orbit.yaml": localBase,
		"orbit.local.yaml": `
services:
  worker: null
  api: { env: { A: null } }
`,
	})
	if cfg.Services["worker"] != nil || slices.Contains(cfg.Order, "worker") {
		t.Fatal("worker should be removed")
	}
	if _, ok := cfg.Services["api"].Env["A"]; ok {
		t.Fatalf("env A should be removed: %v", cfg.Services["api"].Env)
	}
}

func TestLocalInterpolatesAndReportsItsOwnErrors(t *testing.T) {
	cfg, err := LoadConfig(LoadOptions{
		Dir: project(t, map[string]string{
			"orbit.yaml":       localBase,
			"orbit.local.yaml": "services:\n  api: { env: { X: \"${WHO:-nobody}\" } }\n",
		}),
		Env: map[string]string{"WHO": "me"},
	})
	if err != nil || cfg.Services["api"].Env["X"] != "me" {
		t.Fatalf("err = %v", err)
	}
	msg := loadErr(t, map[string]string{"orbit.yaml": localBase, "orbit.local.yaml": "services: [\n"})
	if !strings.Contains(msg, "orbit.local.yaml") {
		t.Fatalf("error should name the local file: %s", msg)
	}
}

func TestLocalIgnoredWithoutOrbitYaml(t *testing.T) {
	cfg, err := LoadConfig(LoadOptions{Dir: project(t, map[string]string{
		"docker-compose.yml": "services:\n  web:\n    image: nginx\n",
		"orbit.local.yaml":   "services:\n  nope: { cmd: x }\n",
	}), Env: noEnv})
	if err != nil || cfg.Services["nope"] != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateReportsUnknownKeysInLocal(t *testing.T) {
	res := ValidateFile(LoadOptions{Dir: project(t, map[string]string{
		"orbit.yaml":       localBase,
		"orbit.local.yaml": "services:\n  api: { helth: false }\n",
	}), Env: noEnv})
	if len(res.Errors) != 1 || !strings.HasPrefix(res.Errors[0], "orbit.local.yaml: ") {
		t.Fatalf("errors = %v", res.Errors)
	}
}
