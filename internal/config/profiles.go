package config

import (
	"fmt"
	"slices"
	"strings"
)

// profileKeys are the keys a profile may set: the ones that do not decide what the project is (its name, which
// compose files it imports), because the state of a running project is stored under them.
var profileKeys = []string{"description", "env", "env_file", "shell", "services", "groups", "tasks"}

// ProfileInfo is a profile declared in orbit.yaml.
type ProfileInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// profiles are the `profiles:` of orbit.yaml: the overlay of each one, without its description.
type profiles struct {
	info   []ProfileInfo
	bodies map[string]*Map
}

func (p *profiles) names() []string {
	out := make([]string, len(p.info))
	for i, in := range p.info {
		out[i] = in.Name
	}
	return out
}

// parseProfiles reads `profiles: { debug: { description, services: {…} } }` out of the document.
func parseProfiles(doc *Map) *profiles {
	p := &profiles{bodies: map[string]*Map{}}
	rec := asRecord(doc.Get("profiles"), "profiles")
	for _, name := range rec.Keys() {
		path := "profiles." + name
		body := asRecord(rec.Get(name), path)
		overlay := NewMap()
		for _, key := range body.Keys() {
			if !slices.Contains(profileKeys, key) {
				fail(fmt.Sprintf("`%s` cannot be set by a profile (allowed: %s)", key, strings.Join(profileKeys[1:], ", ")), path+"."+key)
			}
			if key != "description" {
				overlay.Set(key, body.Get(key))
			}
		}
		desc, _ := asString(body.Get("description"), path+".description")
		p.info = append(p.info, ProfileInfo{Name: name, Description: desc})
		p.bodies[name] = overlay
	}
	return p
}

// activeProfiles decides which profiles apply: --profile, then ORBIT_PROFILES, then what the project had selected
// last time, then `default_profiles`. A name nobody declared is a mistake, except in the saved selection: a profile
// that was deleted since must not keep the project from opening.
func activeProfiles(opts LoadOptions, env map[string]string, root, name string, avail *profiles, doc *Map) []string {
	pick := func(list []string, where string, strict bool) []string {
		var out []string
		for _, n := range list {
			n = strings.TrimSpace(n)
			if n == "" || slices.Contains(out, n) {
				continue
			}
			if _, ok := avail.bodies[n]; !ok {
				if !strict {
					continue
				}
				fail(unknownProfile(n, avail), where)
			}
			out = append(out, n)
		}
		return out
	}
	switch {
	case opts.ProfilesSet:
		return pick(opts.Profiles, "--profile", true)
	case env["ORBIT_PROFILES"] != "":
		return pick(strings.Split(env["ORBIT_PROFILES"], ","), "ORBIT_PROFILES", true)
	}
	if opts.SavedProfiles != nil {
		if saved, ok := opts.SavedProfiles(root, name); ok {
			return pick(saved, "", false)
		}
	}
	return pick(asStringList(doc.Get("default_profiles"), "default_profiles"), "default_profiles", true)
}

func unknownProfile(name string, avail *profiles) string {
	if len(avail.info) == 0 {
		return fmt.Sprintf("profile %q not found: orbit.yaml defines no `profiles:`", name)
	}
	names := avail.names()
	msg := fmt.Sprintf("profile %q not found", name)
	if s := Suggest(name, names); s != "" {
		msg += fmt.Sprintf(" (did you mean %q?)", s)
	}
	return msg + " (profiles: " + strings.Join(names, ", ") + ")"
}

// overlays lays the profiles and then the local file over base. removed tells which source took each service out
// with a `null`, because the services imported from compose are not in the document and so cannot simply vanish
// from it.
type overlays struct {
	doc     *Map
	removed map[string]string
}

func (o *overlays) lay(over *Map, source string) {
	o.doc = mergeDoc(o.doc, over)
	services, _ := over.Get("services").(*Map)
	for _, n := range services.Keys() {
		if services.Get(n) == nil {
			o.removed[n] = source
		} else {
			delete(o.removed, n)
		}
	}
}
