package userconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotificationsSurviveAThemeWrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.MkdirAll(Dir(), 0o755)
	os.WriteFile(filepath.Join(Dir(), "config.json"), []byte(`{"theme":"x","notifications":false}`), 0o644)
	c := Read()
	if c.Theme != "x" || c.NotificationsEnabled() {
		t.Fatalf("read = %+v", c)
	}
	if !Write(Config{Theme: "y"}) {
		t.Fatal("write failed")
	}
	c = Read()
	if c.Theme != "y" || c.NotificationsEnabled() {
		t.Fatalf("after write = %+v", c)
	}
}

func TestDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c := Read()
	if c.Theme != "" || !c.NotificationsEnabled() {
		t.Fatalf("defaults = %+v", c)
	}
}

func TestPluginSettingsRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	off := false
	if !Write(Config{
		Plugins: map[string]PluginSetting{"forge": {Enabled: &off, Settings: map[string]any{"host": "gitlab.example.com"}}},
		Keys:    map[string]string{"forge.merge": "ctrl+m"},
	}) {
		t.Fatal("write failed")
	}
	// a second write of another plugin must not drop the first
	on := true
	Write(Config{Plugins: map[string]PluginSetting{"kube": {Enabled: &on}}, Keys: map[string]string{"kube.pf": "none"}})
	c := Read()
	if f := c.Plugins["forge"]; f.Enabled == nil || *f.Enabled || f.Settings["host"] != "gitlab.example.com" {
		t.Fatalf("forge = %+v", f)
	}
	if k := c.Plugins["kube"]; k.Enabled == nil || !*k.Enabled {
		t.Fatalf("kube = %+v", k)
	}
	if c.Keys["forge.merge"] != "ctrl+m" || c.Keys["kube.pf"] != "none" {
		t.Fatalf("keys = %v", c.Keys)
	}
}

func TestReadIgnoresBadPluginEntries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "orbit"), 0o755)
	os.WriteFile(filepath.Join(dir, "orbit", "config.json"), []byte(`{"theme":"nord","plugins":{"ok":{"enabled":false},"bad":"yes"},"keys":{"a.b":"x","c.d":3}}`), 0o644)
	c := Read()
	if c.Theme != "nord" || c.Plugins["ok"].Enabled == nil || len(c.Plugins) != 1 || len(c.Keys) != 1 {
		t.Fatalf("a bad entry must be skipped, not break the rest: %+v", c)
	}
}
