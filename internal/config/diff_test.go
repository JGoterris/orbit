package config

import (
	"reflect"
	"testing"
)

func svc(name, cmd string, edit ...func(*ServiceConfig)) *ServiceConfig {
	s := &ServiceConfig{
		Name: name, Type: TypeProcess, Cmd: cmd, Cwd: "/w", Env: map[string]string{}, EnvFiles: []EnvFileRef{},
		DependsOn: []string{}, Restart: RestartNo, StartTimeout: 5000, StopTimeout: 1000, Autostart: true,
		LeakDetection: true, Ports: []string{}, Volumes: []string{}, DockerArgs: []string{},
	}
	for _, e := range edit {
		e(s)
	}
	return s
}

func cfg(svcs ...*ServiceConfig) *OrbitConfig {
	c := &OrbitConfig{Name: "t", Root: "/w", Services: map[string]*ServiceConfig{}, Groups: map[string][]string{}}
	for _, s := range svcs {
		c.Services[s.Name] = s
		c.Order = append(c.Order, s.Name)
	}
	return c
}

func TestEqualConfigsHaveEmptyDiff(t *testing.T) {
	a := cfg(svc("a", "x", func(s *ServiceConfig) { s.Env = map[string]string{"A": "1", "B": "2"} }))
	b := cfg(svc("a", "x", func(s *ServiceConfig) { s.Env = map[string]string{"B": "2", "A": "1"} }))
	if !DiffConfig(a, b).IsEmpty() {
		t.Fatal("diff should be empty")
	}
	// nil and empty collections are the same thing
	c := cfg(svc("a", "x", func(s *ServiceConfig) { s.DependsOn = nil }))
	if !DiffConfig(cfg(svc("a", "x")), c).IsEmpty() {
		t.Fatal("nil vs empty slice must not be a change")
	}
}

func TestAddedRemovedChanged(t *testing.T) {
	prev := cfg(svc("a", "x"), svc("b", "y"), svc("c", "z"), svc("d", "w"))
	next := cfg(svc("a", "x2"), svc("c", "z", func(s *ServiceConfig) { s.Description = "hi" }), svc("d", "w"), svc("e", "v"))
	d := DiffConfig(prev, next)
	if !reflect.DeepEqual(d.Added, []string{"e"}) || !reflect.DeepEqual(d.Removed, []string{"b"}) {
		t.Fatalf("added = %v, removed = %v", d.Added, d.Removed)
	}
	want := []ServiceChange{
		{Name: "a", Fields: []string{"cmd"}, Restart: true},
		{Name: "c", Fields: []string{"description"}, Restart: false},
	}
	if !reflect.DeepEqual(d.Changed, want) {
		t.Fatalf("changed = %+v, want %+v", d.Changed, want)
	}
	if got, w := d.Describe(), []string{"+ e", "- b", "~ a (cmd) ↻", "~ c (description)"}; !reflect.DeepEqual(got, w) {
		t.Fatalf("describe = %v, want %v", got, w)
	}
	if !reflect.DeepEqual(d.RestartNames(), []string{"a"}) {
		t.Fatalf("restart names = %v", d.RestartNames())
	}
}

func TestComposeHashChangeShowsUp(t *testing.T) {
	d := DiffConfig(
		cfg(svc("a", "x", func(s *ServiceConfig) { s.ComposeHash = "1" })),
		cfg(svc("a", "x", func(s *ServiceConfig) { s.ComposeHash = "2" })),
	)
	want := []ServiceChange{{Name: "a", Fields: []string{"composeHash"}, Restart: true}}
	if !reflect.DeepEqual(d.Changed, want) {
		t.Fatalf("changed = %+v", d.Changed)
	}
}

func TestRenamedProjectIsReportedNotApplied(t *testing.T) {
	next := cfg(svc("a", "x"))
	next.Name = "other"
	d := DiffConfig(cfg(svc("a", "x")), next)
	if d.NameChanged == nil || d.NameChanged.From != "t" || d.NameChanged.To != "other" {
		t.Fatalf("nameChanged = %+v", d.NameChanged)
	}
}

func TestGroupsChange(t *testing.T) {
	a, b := cfg(svc("a", "x")), cfg(svc("a", "x"))
	b.Groups["all"] = []string{"a"}
	if !DiffConfig(a, b).Groups {
		t.Fatal("a new group must show as a groups change")
	}
}

func TestTasksChangeIsReportedWithoutRestarts(t *testing.T) {
	a, b := cfg(svc("a", "x")), cfg(svc("a", "x"))
	b.Tasks = map[string]*TaskConfig{"lint": {Name: "lint", Cmd: "go vet"}}
	d := DiffConfig(a, b)
	if !d.Tasks || d.IsEmpty() || len(d.RestartNames()) != 0 {
		t.Fatalf("diff = %+v", d)
	}
	if got := d.Describe(); !reflect.DeepEqual(got, []string{"~ tasks"}) {
		t.Fatalf("describe = %v", got)
	}
	// no tasks on either side, nil or empty, is no change
	a.Tasks = map[string]*TaskConfig{}
	b.Tasks = nil
	if !DiffConfig(a, b).IsEmpty() {
		t.Fatal("empty vs nil tasks must not be a change")
	}
}
