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
	// Splash: the animated logo when the TUI opens (on unless false)
	Splash *bool `json:"splash,omitempty"`
	// UpdateCheck: the once-a-day look for a newer release that makes the TUI show a notice (on unless false)
	UpdateCheck *bool `json:"updateCheck,omitempty"`
	// Plugins holds, by plugin name, whether this user turned it off and the settings they gave it
	Plugins map[string]PluginSetting `json:"plugins,omitempty"`
	// Keys rebinds the keys of plugin actions: "<plugin>.<action>" → key ("none" unbinds it)
	Keys map[string]string `json:"keys,omitempty"`
}

// PluginSetting is the entry of a plugin in the plugins of config.json.
type PluginSetting struct {
	// Enabled is nil unless the user said something: plugins are on once installed
	Enabled  *bool          `json:"enabled,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
}

// NotificationsEnabled is whether desktop notifications are on.
func (c Config) NotificationsEnabled() bool { return c.Notifications == nil || *c.Notifications }

// SplashEnabled is whether the TUI opens with its animated logo.
func (c Config) SplashEnabled() bool { return c.Splash == nil || *c.Splash }

// UpdateCheckEnabled is whether the TUI may look for a newer release.
func (c Config) UpdateCheckEnabled() bool { return c.UpdateCheck == nil || *c.UpdateCheck }

// Read returns the saved settings; an empty Config when there are none or the file is unreadable.
func Read() Config {
	data, err := os.ReadFile(filepath.Join(Dir(), "config.json"))
	if err != nil {
		return Config{}
	}
	var raw struct {
		Theme         any                        `json:"theme"`
		Notifications any                        `json:"notifications"`
		Splash        any                        `json:"splash"`
		UpdateCheck   any                        `json:"updateCheck"`
		Plugins       map[string]json.RawMessage `json:"plugins"`
		Keys          map[string]any             `json:"keys"`
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
	if b, ok := raw.Splash.(bool); ok {
		c.Splash = &b
	}
	if b, ok := raw.UpdateCheck.(bool); ok {
		c.UpdateCheck = &b
	}
	for name, entry := range raw.Plugins {
		var ps PluginSetting
		if json.Unmarshal(entry, &ps) == nil {
			if c.Plugins == nil {
				c.Plugins = map[string]PluginSetting{}
			}
			c.Plugins[name] = ps
		}
	}
	for action, key := range raw.Keys {
		if k, ok := key.(string); ok {
			if c.Keys == nil {
				c.Keys = map[string]string{}
			}
			c.Keys[action] = k
		}
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

// Forget removes what config.json says about a plugin: its entry and the keys the user gave its actions.
func Forget(plugin string) bool {
	cur := Read()
	delete(cur.Plugins, plugin)
	for k := range cur.Keys {
		if len(k) > len(plugin) && k[:len(plugin)+1] == plugin+"." {
			delete(cur.Keys, k)
		}
	}
	return WriteJSONAtomic(filepath.Join(Dir(), "config.json"), cur)
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
	if patch.Splash != nil {
		cur.Splash = patch.Splash
	}
	if patch.UpdateCheck != nil {
		cur.UpdateCheck = patch.UpdateCheck
	}
	for name, ps := range patch.Plugins {
		if cur.Plugins == nil {
			cur.Plugins = map[string]PluginSetting{}
		}
		cur.Plugins[name] = ps
	}
	for action, key := range patch.Keys {
		if cur.Keys == nil {
			cur.Keys = map[string]string{}
		}
		cur.Keys[action] = key
	}
	return WriteJSONAtomic(filepath.Join(Dir(), "config.json"), cur)
}
