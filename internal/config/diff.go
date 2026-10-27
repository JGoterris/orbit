package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

type ServiceChange struct {
	Name string `json:"name"`
	// Fields are the keys of the service config that differ
	Fields []string `json:"fields"`
	// Restart: a running instance has to be restarted to pick the change up
	Restart bool `json:"restart"`
}

type NameChange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ConfigDiff struct {
	Added   []string        `json:"added"`
	Removed []string        `json:"removed"`
	Changed []ServiceChange `json:"changed"`
	// Groups: the `groups:` section differs (applied without restarting anything)
	Groups bool `json:"groups"`
	// Tasks: the `tasks:` section differs (applied without restarting anything)
	Tasks bool `json:"tasks"`
	// Plugins: the `plugins:` section differs; plugins start with orbit, so it takes effect after `orbit down`
	Plugins bool `json:"plugins,omitempty"`
	// Profiles: a different set of profiles is active (applied without restarting anything by itself)
	Profiles bool `json:"profiles"`
	// ActiveProfiles is the new set when Profiles is true
	ActiveProfiles []string `json:"activeProfiles,omitempty"`
	// NameChanged: the project name is part of the state dir and the socket, so it cannot change while orbit runs
	NameChanged *NameChange `json:"nameChanged,omitempty"`
}

// hot are the changes that take effect on a running service without restarting it.
var hot = map[string]bool{
	"description": true, "url": true, "console": true, "autostart": true, "restart": true, "memLimit": true,
	"leakDetection": true, "startTimeout": true, "stopTimeout": true, "health": true, "watch": true,
}

// jsonFields are the json names of ServiceConfig, in declaration order.
var jsonFields = func() []string {
	t := reflect.TypeOf(ServiceConfig{})
	out := make([]string, t.NumField())
	for i := range out {
		out[i], _, _ = strings.Cut(t.Field(i).Tag.Get("json"), ",")
	}
	return out
}()

// fieldKey is a comparable form of a field: JSON (maps come out with sorted keys, so two equal values compare equal
// whatever the order they were built in), with nil and empty collections treated alike.
func fieldKey(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		if v.Len() == 0 {
			return "empty"
		}
	}
	b, _ := json.Marshal(v.Interface())
	return string(b)
}

func changedFields(a, b *ServiceConfig) []string {
	va, vb := reflect.ValueOf(*a), reflect.ValueOf(*b)
	var out []string
	for i, name := range jsonFields {
		if fieldKey(va.Field(i)) != fieldKey(vb.Field(i)) {
			out = append(out, name)
		}
	}
	return out
}

func stableJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// DiffConfig compares two configs: what was added, removed and changed, and whether each change needs a restart.
func DiffConfig(prev, next *OrbitConfig) ConfigDiff {
	d := ConfigDiff{Added: []string{}, Removed: []string{}, Changed: []ServiceChange{}}
	for _, n := range next.Order {
		if prev.Services[n] == nil {
			d.Added = append(d.Added, n)
		}
	}
	for _, n := range prev.Order {
		if next.Services[n] == nil {
			d.Removed = append(d.Removed, n)
		}
	}
	for _, name := range next.Order {
		before := prev.Services[name]
		if before == nil {
			continue
		}
		fields := changedFields(before, next.Services[name])
		if len(fields) == 0 {
			continue
		}
		restart := false
		for _, f := range fields {
			if !hot[f] {
				restart = true
			}
		}
		d.Changed = append(d.Changed, ServiceChange{Name: name, Fields: fields, Restart: restart})
	}
	d.Groups = stableJSON(normGroups(prev.Groups)) != stableJSON(normGroups(next.Groups))
	d.Tasks = (len(prev.Tasks) > 0 || len(next.Tasks) > 0) && stableJSON(prev.Tasks) != stableJSON(next.Tasks)
	d.Plugins = (len(prev.Plugins) > 0 || len(next.Plugins) > 0) && stableJSON(prev.Plugins) != stableJSON(next.Plugins)
	if d.Profiles = !slices.Equal(prev.Profiles, next.Profiles); d.Profiles {
		d.ActiveProfiles = next.Profiles
	}
	if prev.Name != next.Name {
		d.NameChanged = &NameChange{From: prev.Name, To: next.Name}
	}
	return d
}

func normGroups(g map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range g {
		out[k] = orEmpty(v)
	}
	return out
}

func (d ConfigDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Changed) == 0 && !d.Groups && !d.Tasks && !d.Plugins && !d.Profiles && d.NameChanged == nil
}

// RestartNames are the services a running instance of prev has to restart for d (the changed ones that need it).
func (d ConfigDiff) RestartNames() []string {
	var out []string
	for _, c := range d.Changed {
		if c.Restart {
			out = append(out, c.Name)
		}
	}
	return out
}

// Describe is one line per difference, compose-style: `+ api`, `- worker`, `~ web (cmd, env) ↻`.
func (d ConfigDiff) Describe() []string {
	var out []string
	for _, n := range d.Added {
		out = append(out, "+ "+n)
	}
	for _, n := range d.Removed {
		out = append(out, "- "+n)
	}
	for _, c := range d.Changed {
		line := fmt.Sprintf("~ %s (%s)", c.Name, strings.Join(c.Fields, ", "))
		if c.Restart {
			line += " ↻"
		}
		out = append(out, line)
	}
	if d.Groups {
		out = append(out, "~ groups")
	}
	if d.Tasks {
		out = append(out, "~ tasks")
	}
	if d.Plugins {
		out = append(out, "! plugins: not applied while running (`orbit down` and reopen)")
	}
	if d.Profiles {
		out = append(out, "~ profiles: "+profilesLabel(d.ActiveProfiles))
	}
	if d.NameChanged != nil {
		out = append(out, fmt.Sprintf("! name %s → %s: not applied while running (`orbit down` and reopen)", d.NameChanged.From, d.NameChanged.To))
	}
	return out
}

// profilesLabel is a set of profiles as the user reads it.
func profilesLabel(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
