package plugins

import (
	"encoding/json"
	"maps"
	"os"
	"slices"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/userconfig"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

func init() { config.PluginTypes = installedTypes }

func installedTypes() map[string]string {
	list, _ := Installed()
	var out map[string]string
	for _, p := range list {
		for _, t := range p.Manifest.Contributes.ServiceTypes {
			if out == nil {
				out = map[string]string{}
			}
			if _, taken := out[t.Name]; !taken { // the first plugin (by name) keeps a type two of them provide
				out[t.Name] = p.Manifest.Name
			}
		}
	}
	return out
}

// Resolve decides which installed plugins run for a project. A plugin runs unless the user (config.json) or the
// project (`plugins: { name: false }`) turned it off; the settings of the user and then the project are merged, the
// project's winning key by key. Missing are the plugins the project asks for that cannot run: not installed, or
// installed but broken.
func Resolve(cfg *config.OrbitConfig, user userconfig.Config) (specs []Spec, missing []string) {
	installed, _ := Installed()
	byName := map[string]*Plugin{}
	for _, p := range installed {
		byName[p.Manifest.Name] = p
	}
	for _, p := range installed {
		name := p.Manifest.Name
		u, hasUser := user.Plugins[name]
		pc, hasProject := cfg.Plugins[name]
		if hasUser && u.Enabled != nil && !*u.Enabled {
			continue
		}
		if hasProject && !pc.Enabled {
			continue
		}
		settings := map[string]any{}
		maps.Copy(settings, u.Settings)
		if hasProject {
			maps.Copy(settings, pc.Settings)
		}
		var raw json.RawMessage
		if len(settings) > 0 {
			raw, _ = json.Marshal(settings)
		}
		specs = append(specs, Spec{Plugin: p, Settings: raw})
	}
	for name, pc := range cfg.Plugins {
		if pc.Enabled && byName[name] == nil {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return specs, missing
}

// ForProject is the Manager of the plugins of cfg, not started. ORBIT_NO_PLUGINS turns every plugin off.
func ForProject(cfg *config.OrbitConfig, version string) *Manager {
	opts := Options{Version: version, Project: cfg.Name, ProjectDir: cfg.Root, Services: ServiceRefs(cfg)}
	if os.Getenv("ORBIT_NO_PLUGINS") == "" {
		opts.Specs, opts.Missing = Resolve(cfg, userconfig.Read())
		opts.MissingFrom = map[string]string{}
		for _, name := range opts.Missing {
			opts.MissingFrom[name] = cfg.Plugins[name].From
		}
	}
	return NewManager(opts)
}

func ServiceRefs(cfg *config.OrbitConfig) []protocol.ServiceRef {
	var out []protocol.ServiceRef
	for _, name := range cfg.Order {
		if svc := cfg.Services[name]; svc != nil {
			out = append(out, protocol.ServiceRef{Name: name, Type: string(svc.Type), Cwd: svc.Cwd})
		}
	}
	return out
}
