package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/userconfig"
)

func linkNamed(t *testing.T, name, extra string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	must(t, os.MkdirAll(dir, 0o755))
	manifest := strings.NewReplacer("demo", name).Replace(testManifest) + extra
	must(t, os.WriteFile(filepath.Join(dir, "orbit-plugin.yaml"), []byte(manifest), 0o644))
	if _, err := Link(dir); err != nil {
		t.Fatal(err)
	}
}

func names(specs []Spec) []string {
	var out []string
	for _, s := range specs {
		out = append(out, s.Plugin.Manifest.Name)
	}
	return out
}

func TestResolveActivation(t *testing.T) {
	isolate(t)
	linkNamed(t, "alpha", "")
	linkNamed(t, "beta", "")
	linkNamed(t, "gamma", "")
	off := false
	user := userconfig.Config{Plugins: map[string]userconfig.PluginSetting{
		"beta":  {Enabled: &off},
		"alpha": {Settings: map[string]any{"a": 1, "shared": "user"}},
	}}
	cfg := &config.OrbitConfig{Plugins: map[string]*config.PluginConfig{
		"gamma":  {Enabled: false},
		"alpha":  {Enabled: true, Settings: map[string]any{"shared": "project"}},
		"absent": {Enabled: true},
		"off":    {Enabled: false},
	}}
	specs, missing := Resolve(cfg, user)
	if got := names(specs); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("running = %v, want only alpha (beta is off for the user, gamma for the project)", got)
	}
	if len(missing) != 1 || missing[0] != "absent" {
		t.Fatalf("missing = %v: only a required plugin that is not installed counts", missing)
	}
	var settings map[string]any
	must(t, json.Unmarshal(specs[0].Settings, &settings))
	if settings["a"] != float64(1) || settings["shared"] != "project" {
		t.Fatalf("settings must be merged with the project winning: %v", settings)
	}
}

func TestResolveRunsEverythingInstalledByDefault(t *testing.T) {
	isolate(t)
	linkNamed(t, "alpha", "")
	linkNamed(t, "beta", "")
	specs, missing := Resolve(&config.OrbitConfig{}, userconfig.Config{})
	if len(specs) != 2 || len(missing) != 0 {
		t.Fatalf("specs = %v missing = %v", names(specs), missing)
	}
}

func TestInstalledServiceTypesReachTheConfig(t *testing.T) {
	isolate(t)
	linkNamed(t, "kube", "contributes:\n  serviceTypes:\n    - { name: k8s }\n")
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "orbit.yaml"), []byte("services:\n  w: { type: k8s, with: { ctx: a } }\n"), 0o644))
	cfg, err := config.LoadConfig(config.LoadOptions{Dir: dir, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["w"].Type != "k8s" {
		t.Fatalf("type = %q", cfg.Services["w"].Type)
	}
}

func TestNoPluginsSwitch(t *testing.T) {
	isolate(t)
	linkNamed(t, "alpha", "")
	t.Setenv("ORBIT_NO_PLUGINS", "1")
	m := ForProject(&config.OrbitConfig{Name: "p", Root: t.TempDir()}, "1.0.0")
	if len(m.Plugins()) != 0 {
		t.Fatal("ORBIT_NO_PLUGINS must turn every plugin off")
	}
}
