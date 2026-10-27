package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var pluginNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func pluginNew(name string) int {
	if !pluginNamePattern.MatchString(name) {
		errorln(fmt.Sprintf("%q is not a plugin name: lowercase letters, digits and dashes, starting with a letter", name))
		return 1
	}
	if err := checkName(name); err != nil {
		errorln(err.Error())
		return 1
	}
	dir := "orbit-" + name
	if _, err := os.Stat(dir); err == nil {
		errorln(dir + " already exists")
		return 1
	}
	files, err := scaffold(name)
	if err != nil {
		errorln(err.Error())
		return 1
	}
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			errorln(err.Error())
			return 1
		}
		mode := os.FileMode(0o644)
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			errorln(err.Error())
			return 1
		}
	}
	fmt.Println(green("created " + dir + "/"))
	fmt.Printf("\n  cd %s\n  go mod tidy\n  make link        %s\n\nthen open a project with orbit and press the number of the new tab.\n", dir, dim("# builds it and runs `orbit plugin link .`"))
	fmt.Println(dim("\nAfter `make link`, a change to the code needs `make` and a restart of orbit (`orbit down` first if it runs)."))
	return 0
}

func scaffold(name string) (map[string]string, error) {
	title := strings.ToUpper(name[:1]) + name[1:]
	r := strings.NewReplacer("@NAME@", name, "@TITLE@", title)
	out := map[string]string{}
	for path, tmpl := range scaffoldFiles {
		out[path] = r.Replace(tmpl)
	}
	return out, nil
}

var scaffoldFiles = map[string]string{
	"orbit-plugin.yaml": `name: @NAME@
version: 0.1.0
api: 1
description: What @NAME@ adds to orbit
run: { command: [./orbit-@NAME@, serve], windows: [./orbit-@NAME@.exe, serve] }
cli: { command: [./orbit-@NAME@], windows: [./orbit-@NAME@.exe] }
contributes:
  views:
    - id: main
      title: @TITLE@
      panes:
        - { id: list, widget: list, title: @TITLE@, weight: 2 }
        - { id: info, widget: detail, title: Details, bind: list }
  actions:
    - id: refresh
      title: Refresh
      key: R
      scope: { view: main, pane: list }
`,
	"main.go": `// Command orbit-@NAME@ is a plugin for orbit.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/JGoterris/orbit/pluginsdk"
	"github.com/JGoterris/orbit/pluginsdk/protocol"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := serve(); err != nil {
			fmt.Fprintln(os.Stderr, "orbit-@NAME@:", err)
			os.Exit(1)
		}
		return
	}
	// ` + "`orbit @NAME@ …`" + ` runs this, with the arguments as they were typed
	fmt.Println("orbit @NAME@: hello from the command line")
}

func serve() error {
	p := pluginsdk.New()

	// what the list of the "main" view shows; orbit draws it, you only say what is in it
	p.Pane("main", "list", func(r pluginsdk.Request) (protocol.ViewData, error) {
		columns := []protocol.Column{{ID: "name"}, {ID: "when", Priority: 1, Width: 10}}
		return pluginsdk.ListData(columns,
			pluginsdk.Row("hello", "ok", map[string]protocol.Cell{
				"name": pluginsdk.Cell("Hello from @NAME@"),
				"when": pluginsdk.Cell(time.Now().Format("15:04:05"), protocol.ToneMuted),
			}),
		), nil
	})

	// the detail follows the selection of the list (bind: list in the manifest)
	p.Pane("main", "info", func(r pluginsdk.Request) (protocol.ViewData, error) {
		return pluginsdk.DetailData(protocol.Section{
			Title: "item " + r.Context.Selection["list"],
			Fields: []protocol.Field{
				pluginsdk.Field("service", r.Context.Service),
				pluginsdk.Field("project", os.Getenv("ORBIT_PROJECT_DIR"), protocol.ToneMuted),
			},
		}), nil
	})

	p.Action("refresh", func(r pluginsdk.ActionRequest) (*protocol.InvokeResult, error) {
		return pluginsdk.Done(protocol.ToneOK, "refreshed"), nil
	})

	return p.Run()
}
`,
	"go.mod": `module github.com/your-name/orbit-@NAME@

go 1.26
`,
	"Makefile": `.PHONY: build link
build:
	go build -o orbit-@NAME@ .

# builds it and uses this folder as the plugin, so every change is seen after a restart of orbit
link: build
	orbit plugin link .
`,
	".gitignore": "orbit-@NAME@\norbit-@NAME@.exe\ndist/\n",
	".goreleaser.yaml": `version: 2
project_name: orbit-@NAME@
builds:
  - main: .
    binary: orbit-@NAME@
    env: [CGO_ENABLED=0]
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
archives:
  - formats: [tar.gz]
    format_overrides:
      - { goos: windows, formats: [zip] }
    # orbit looks for orbit-@NAME@_<os>_<arch>.tar.gz (.zip on Windows) in the release
    name_template: "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"
    files:
      - orbit-plugin.yaml
checksum:
  name_template: checksums.txt
`,
	".github/workflows/release.yml": `name: release
on:
  push:
    tags: ["v*"]
permissions:
  contents: write
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod }
      - uses: goreleaser/goreleaser-action@v6
        with: { args: release --clean }
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
`,
	"README.md": `# orbit-@NAME@

A plugin for [orbit](https://github.com/JGoterris/orbit).

## Install

` + "```" + `bash
orbit plugin install your-name/orbit-@NAME@
` + "```" + `

## Develop

` + "```" + `bash
go mod tidy
make link      # builds and uses this folder as the plugin
` + "```" + `

Open a project with orbit: the plugin adds a tab. After a change, ` + "`make`" + ` and restart orbit
(` + "`orbit down`" + ` first if it was running). ` + "`orbit plugin logs @NAME@`" + ` shows what the plugin printed.

## Publish

Push a tag (` + "`git tag v0.1.0 && git push --tags`" + `): the workflow builds a release with one archive per platform and a
` + "`checksums.txt`" + `, which is what ` + "`orbit plugin install`" + ` downloads. Add the topic ` + "`orbit-plugin`" + ` to the repository so
people can find it.
`,
}
