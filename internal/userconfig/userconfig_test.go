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
