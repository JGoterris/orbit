package plugins

import (
	"path/filepath"
	"strings"
	"testing"
)

const goodManifest = `
name: forge
version: 0.1.0
api: 1
description: MR and CI
run: { command: [./orbit-forge, serve] }
cli: { command: [./orbit-forge] }
events: [state, config]
contributes:
  views:
    - id: prs
      title: Pull requests
      panes:
        - { id: list, widget: list, title: MR, weight: 2 }
        - { id: info, widget: detail, bind: list }
        - { id: log, widget: text, bind: list, follow: true }
  actions:
    - id: merge
      title: Merge
      key: M
      scope: { view: prs, pane: list }
      confirm: { title: "Merge {item}?", danger: true }
      inputs:
        - { name: method, kind: pick, options: [squash, merge] }
    - id: open
      title: Open
      key: alt+o
  serviceSections: [{ id: pr, title: PR }]
  serviceColumn: { id: pr, width: 8 }
  headerBadges: true
  serviceTypes:
    - { name: k8s, badge: k8s }
`

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest([]byte(goodManifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "forge" || len(m.Contributes.Views[0].Panes) != 3 || m.Contributes.Actions[0].Confirm == nil {
		t.Fatalf("parsed wrong: %+v", m)
	}
}

func TestParseManifestRejects(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(goodManifest, old, new, 1) }
	cases := map[string]struct{ yaml, want string }{
		"bad name":         {replace("name: forge", "name: Forge!"), "name"},
		"no version":       {replace("version: 0.1.0", "version: ''"), "version"},
		"future api":       {replace("api: 1", "api: 9"), "api"},
		"unknown key":      {replace("description: MR and CI", "descripton: x"), "descripton"},
		"unknown event":    {replace("[state, config]", "[state, boot]"), "boot"},
		"unknown widget":   {replace("widget: detail", "widget: chart"), "chart"},
		"bind forward":     {replace("bind: list }", "bind: log }"), "declared before"},
		"global plain key": {replace("key: alt+o", "key: o"), "alt+<key>"},
		"bad key":          {replace("key: M", "key: 'ctrl+ +'"), "key"},
		"action no view":   {replace("view: prs", "view: nope"), "nope"},
		"action no pane":   {replace("pane: list }", "pane: nope }"), "nope"},
		"pick no options":  {replace("options: [squash, merge]", "title: x"), "without options"},
		"core type":        {replace("name: k8s", "name: docker"), "docker"},
		"no run":           {replace("run: { command: [./orbit-forge, serve] }", ""), "run.command"},
		"empty run":        {replace("[./orbit-forge, serve]", "[]"), "empty"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseManifest([]byte(c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want an error mentioning %q, got %v", c.want, err)
			}
		})
	}
}

func TestMinOrbit(t *testing.T) {
	Configure("1.0.0")
	t.Cleanup(func() { Configure("") })
	_, err := ParseManifest([]byte(strings.Replace(goodManifest, "api: 1", "api: 1\nminOrbit: 1.2.0", 1)))
	if err == nil || !strings.Contains(err.Error(), "1.2.0") {
		t.Fatalf("minOrbit not enforced: %v", err)
	}
	Configure("1.2.0")
	if _, err := ParseManifest([]byte(strings.Replace(goodManifest, "api: 1", "api: 1\nminOrbit: 1.2.0", 1))); err != nil {
		t.Fatal(err)
	}
}

func TestArgvResolvesRelativePaths(t *testing.T) {
	m, err := ParseManifest([]byte(goodManifest))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("some", "dir")
	got := Argv(m.Run, dir)
	if got[0] != filepath.Join(dir, "orbit-forge") || got[1] != "serve" {
		t.Fatalf("argv = %v", got)
	}
	if Argv(nil, dir) != nil {
		t.Fatal("nil command must give no argv")
	}
}
