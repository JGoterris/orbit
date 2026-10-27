// Package plugins loads, installs and runs the plugins of orbit: separate processes that contribute views,
// actions, badges and service types, and whose data orbit draws with its own widgets.
package plugins

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JGoterris/orbit/internal/selfupdate"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

var (
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	fKey        = regexp.MustCompile(`^f([1-9]|1[0-2])$`)
	keyPattern  = regexp.MustCompile(`^((ctrl|alt|shift)\+)*([^\s+]|[a-z0-9]+|f([1-9]|1[0-2])|enter|esc|tab|space|up|down|left|right|home|end|pgup|pgdown|delete|backspace)$`)
)

// coreTypes are the service types of orbit itself; a plugin cannot take them over.
var coreTypes = []string{"process", "docker", "compose", "external"}

func ReadManifest(dir string) (*protocol.Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, protocol.ManifestFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%s has no %s", dir, protocol.ManifestFile)
		}
		return nil, err
	}
	return ParseManifest(data)
}

// ParseManifest parses and validates a manifest; unknown keys are an error, so a typo is not silently ignored.
func ParseManifest(data []byte) (*protocol.Manifest, error) {
	var m protocol.Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", protocol.ManifestFile, err)
	}
	if err := ValidateManifest(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", protocol.ManifestFile, err)
	}
	return &m, nil
}

func ValidateManifest(m *protocol.Manifest) error {
	if !namePattern.MatchString(m.Name) {
		return fmt.Errorf("name %q must be lowercase letters, digits and dashes, starting with a letter", m.Name)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("version is required")
	}
	if m.API != protocol.API {
		return fmt.Errorf("api is %d, this orbit speaks api %d", m.API, protocol.API)
	}
	if m.MinOrbit != "" {
		if cur := currentVersion; cur != "" && selfupdate.Newer(m.MinOrbit, cur) {
			return fmt.Errorf("needs orbit %s or newer (this is %s)", m.MinOrbit, cur)
		}
	}
	for what, c := range map[string]*protocol.Command{"run": m.Run, "cli": m.CLI} {
		if c != nil && len(c.Command) == 0 {
			return fmt.Errorf("%s.command is empty", what)
		}
	}
	c := m.Contributes
	needsRun := len(c.Views) > 0 || len(c.Actions) > 0 || len(c.ServiceSections) > 0 || c.ServiceColumn != nil ||
		c.HeaderBadges || len(c.ServiceTypes) > 0 || len(m.Events) > 0
	if needsRun && m.Run == nil {
		return fmt.Errorf("it contributes to the interface, so it needs a run.command")
	}
	if m.Run == nil && m.CLI == nil {
		return fmt.Errorf("it has neither run nor cli: there is nothing to start")
	}
	for _, e := range m.Events {
		if !slices.Contains([]string{protocol.EventState, protocol.EventConfig, protocol.EventLog}, e) {
			return fmt.Errorf("unknown event %q (state, config or log)", e)
		}
	}
	views := map[string]protocol.View{}
	for _, v := range c.Views {
		if err := validateView(v, views); err != nil {
			return fmt.Errorf("view %q: %w", v.ID, err)
		}
		views[v.ID] = v
	}
	seen := map[string]bool{}
	for _, a := range c.Actions {
		if seen[a.ID] {
			return fmt.Errorf("action %q is declared twice", a.ID)
		}
		seen[a.ID] = true
		if err := validateAction(a, views); err != nil {
			return fmt.Errorf("action %q: %w", a.ID, err)
		}
	}
	seen = map[string]bool{}
	for _, s := range c.ServiceSections {
		if !idPattern.MatchString(s.ID) || strings.TrimSpace(s.Title) == "" || seen[s.ID] {
			return fmt.Errorf("serviceSections: %q needs a unique id and a title", s.ID)
		}
		seen[s.ID] = true
	}
	if c.ServiceColumn != nil && !idPattern.MatchString(c.ServiceColumn.ID) {
		return fmt.Errorf("serviceColumn: invalid id %q", c.ServiceColumn.ID)
	}
	seen = map[string]bool{}
	for _, t := range c.ServiceTypes {
		if !namePattern.MatchString(t.Name) || slices.Contains(coreTypes, t.Name) || seen[t.Name] {
			return fmt.Errorf("serviceTypes: %q is not a valid, free type name", t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}

func validateView(v protocol.View, done map[string]protocol.View) error {
	if !idPattern.MatchString(v.ID) {
		return fmt.Errorf("invalid id")
	}
	if _, dup := done[v.ID]; dup {
		return fmt.Errorf("declared twice")
	}
	if strings.TrimSpace(v.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if v.Sidebar != "" && v.Sidebar != protocol.SidebarNone && v.Sidebar != protocol.SidebarServices {
		return fmt.Errorf("sidebar must be none or services")
	}
	if len(v.Panes) == 0 || len(v.Panes) > 6 {
		return fmt.Errorf("it needs between 1 and 6 panes")
	}
	ids := map[string]bool{}
	for _, p := range v.Panes {
		if !idPattern.MatchString(p.ID) || ids[p.ID] {
			return fmt.Errorf("pane %q needs a unique id", p.ID)
		}
		if !slices.Contains([]string{protocol.WidgetList, protocol.WidgetDetail, protocol.WidgetText}, p.Widget) {
			return fmt.Errorf("pane %q: widget %q must be list, detail or text", p.ID, p.Widget)
		}
		if p.Bind != "" && !ids[p.Bind] {
			return fmt.Errorf("pane %q binds to %q, which must be declared before it", p.ID, p.Bind)
		}
		if p.Weight < 0 {
			return fmt.Errorf("pane %q: negative weight", p.ID)
		}
		ids[p.ID] = true
	}
	return nil
}

func validateAction(a protocol.Action, views map[string]protocol.View) error {
	if !idPattern.MatchString(a.ID) {
		return fmt.Errorf("invalid id")
	}
	if strings.TrimSpace(a.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if a.Key != "" && !keyPattern.MatchString(a.Key) {
		return fmt.Errorf("key %q is not a key orbit understands", a.Key)
	}
	if a.Scope.Pane != "" && a.Scope.View == "" {
		return fmt.Errorf("scope.pane needs scope.view")
	}
	// an action of no view can be fired from any of them: its key must not be one orbit uses
	if a.Key != "" && a.Scope.View == "" && !strings.HasPrefix(a.Key, "alt+") && !fKey.MatchString(a.Key) {
		return fmt.Errorf("key %q works in every view, so it must be alt+<key> or f1-f12 (or give the action a scope.view)", a.Key)
	}
	if a.Scope.View != "" {
		v, ok := views[a.Scope.View]
		if !ok {
			return fmt.Errorf("scope.view %q is not a view of this plugin", a.Scope.View)
		}
		if a.Scope.Pane != "" && !slices.ContainsFunc(v.Panes, func(p protocol.Pane) bool { return p.ID == a.Scope.Pane }) {
			return fmt.Errorf("scope.pane %q is not a pane of view %q", a.Scope.Pane, a.Scope.View)
		}
	}
	names := map[string]bool{}
	for _, in := range a.Inputs {
		if !idPattern.MatchString(in.Name) || names[in.Name] {
			return fmt.Errorf("input %q needs a unique name", in.Name)
		}
		names[in.Name] = true
		switch in.Kind {
		case protocol.InputText:
		case protocol.InputPick:
			if len(in.Options) == 0 && !in.OptionsFrom {
				return fmt.Errorf("input %q is a pick without options", in.Name)
			}
		default:
			return fmt.Errorf("input %q: kind must be text or pick", in.Name)
		}
	}
	return nil
}

var currentVersion string

func Configure(orbitVersion string) { currentVersion = strings.TrimPrefix(orbitVersion, "v") }

// Argv is the command to run in this OS, with ./ paths resolved against the plugin folder dir.
func Argv(c *protocol.Command, dir string) []string {
	if c == nil {
		return nil
	}
	argv := c.Command
	if runtime.GOOS == "windows" && len(c.Windows) > 0 {
		argv = c.Windows
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		if strings.HasPrefix(a, "./") || strings.HasPrefix(a, `.\`) {
			a = filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(a[2:], `\`, "/")))
		}
		out[i] = a
	}
	return out
}

// Summary says in a few lines what a plugin adds to orbit: what the user is shown before installing it.
func Summary(m *protocol.Manifest) []string {
	var out []string
	c := m.Contributes
	for _, v := range c.Views {
		out = append(out, fmt.Sprintf("a tab %q with %d pane(s)", v.Title, len(v.Panes)))
	}
	if n := len(c.Actions); n > 0 {
		out = append(out, fmt.Sprintf("%d action(s) in the command palette", n))
	}
	if len(c.ServiceSections) > 0 {
		out = append(out, "a block in the detail of every service")
	}
	if c.ServiceColumn != nil {
		out = append(out, "a column in the list of services")
	}
	if c.HeaderBadges {
		out = append(out, "badges in the header")
	}
	for _, t := range c.ServiceTypes {
		out = append(out, fmt.Sprintf("the service type %q", t.Name))
	}
	if m.CLI != nil {
		out = append(out, fmt.Sprintf("the command `orbit %s`", m.Name))
	}
	if len(m.Events) > 0 {
		out = append(out, "it is told about: "+strings.Join(m.Events, ", "))
	}
	return out
}
