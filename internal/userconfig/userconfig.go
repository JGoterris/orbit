// Package userconfig reads and writes the per-user (not per-project) settings:
// $XDG_CONFIG_HOME/orbit, ~/.config/orbit or %APPDATA%\orbit.
package userconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"

	"github.com/JGoterris/orbit/internal/platform"
)

// Dir is the directory of orbit's per-user settings.
func Dir() string { return filepath.Join(platform.ConfigBase(), "orbit") }

// Config is config.json.
type Config struct {
	Theme string `json:"theme,omitempty"`
	// Notifications: desktop notifications when a service goes down (on unless false)
	Notifications *bool `json:"notifications,omitempty"`
}

// NotificationsEnabled is whether desktop notifications are on.
func (c Config) NotificationsEnabled() bool { return c.Notifications == nil || *c.Notifications }

// Read returns the saved settings; an empty Config when there are none or the file is unreadable.
func Read() Config {
	data, err := os.ReadFile(filepath.Join(Dir(), "config.json"))
	if err != nil {
		return Config{}
	}
	var raw struct {
		Theme         any `json:"theme"`
		Notifications any `json:"notifications"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return Config{}
	}
	var c Config
	if s, ok := raw.Theme.(string); ok {
		c.Theme = s
	}
	if b, ok := raw.Notifications.(bool); ok {
		c.Notifications = &b
	}
	return c
}

// WriteJSONAtomic writes data as JSON next to its final path and renames it into place (never leaves a
// half-written file). It reports whether it worked.
func WriteJSONAtomic(file string, data any) bool {
	if os.MkdirAll(filepath.Dir(file), 0o755) != nil {
		return false
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return false
	}
	tmp := file + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o644) != nil {
		return false
	}
	if os.Rename(tmp, file) != nil {
		os.Remove(tmp)
		return false
	}
	return true
}

// Write merges patch into config.json: only the fields that are set replace the saved ones.
func Write(patch Config) bool {
	cur := Read()
	if patch.Theme != "" {
		cur.Theme = patch.Theme
	}
	if patch.Notifications != nil {
		cur.Notifications = patch.Notifications
	}
	return WriteJSONAtomic(filepath.Join(Dir(), "config.json"), cur)
}
