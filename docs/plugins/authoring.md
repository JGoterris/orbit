# Writing a plugin

A plugin is a folder with an `orbit-plugin.yaml` and the program it runs. The manifest says **what the plugin
contributes**; the program answers when orbit asks. Everything is drawn by orbit.

```bash
orbit plugin new weather      # creates orbit-weather/ with a working plugin in Go
cd orbit-weather
go mod tidy
make link                     # builds it and uses the folder as the plugin
```

Open a project with orbit: there is a new tab. After a change, build again and restart orbit (`orbit down` first if it
runs). `orbit plugin logs weather -f` shows what the plugin prints on stderr.

## The manifest

```yaml
name: weather
version: 0.1.0
api: 1
run: { command: [./orbit-weather, serve], windows: [./orbit-weather.exe, serve] }
contributes:
  views:
    - id: main
      title: Weather
      panes:
        - { id: list, widget: list, title: Cities, weight: 2 }
        - { id: info, widget: detail, title: Details, bind: list }
  actions:
    - id: refresh
      title: Refresh
      key: R
      scope: { view: main, pane: list }
```

What a plugin contributes is declared here, not at run time: orbit can draw the tab and register the keys before the
program answers, and whoever installs the plugin sees what it adds. See the [manifest reference](manifest.md).

## The program

With the Go SDK (`github.com/JGoterris/orbit/pluginsdk`) a plugin is a handful of functions:

```go
p := pluginsdk.New()

p.Pane("main", "list", func(r pluginsdk.Request) (protocol.ViewData, error) {
    return pluginsdk.ListData(
        []protocol.Column{{ID: "city"}, {ID: "temp", Priority: 1, Width: 6}},
        pluginsdk.Row("madrid", "ok", map[string]protocol.Cell{
            "city": pluginsdk.Cell("Madrid"),
            "temp": pluginsdk.Cell("21°", protocol.ToneWarn),
        }),
    ), nil
})

p.Pane("main", "info", func(r pluginsdk.Request) (protocol.ViewData, error) {
    city := r.Context.Selection["list"] // the id of the selected row of the pane "list"
    return pluginsdk.DetailData(protocol.Section{
        Title:  city,
        Fields: []protocol.Field{pluginsdk.Field("wind", "12 km/h")},
    }), nil
})

p.Action("refresh", func(r pluginsdk.ActionRequest) (*protocol.InvokeResult, error) {
    return pluginsdk.Done(protocol.ToneOK, "refreshed"), nil
})

return p.Run()
```

- **A pane** answers with the data of its widget: a [list, a detail or text](widgets.md). orbit keeps the selection, the
  scroll and the filter; the plugin never draws, and cannot use colours, only [tones](widgets.md#tones).
- **Context**: every request says where the user is: the selected service and the selected row of each pane. A pane with
  `bind: list` is asked again when that selection changes.
- **An action** returns a message and, optionally, the views to reload (`"view"` or `"view/pane"`; by default all of
  the plugin's, `"none"` for nothing). Its `confirm` and `inputs` in the manifest make orbit ask first, with its own
  dialogs; the answers arrive in `r.Inputs`.
- **On its own**, the plugin can call `p.Invalidate(view, pane)` when its data changed, `p.Toast(tone, text)`,
  `p.SetServiceBadges(service, badges…)` and `p.SetHeaderBadges(badges…)`.
- **Events**: list `events: [state]` and implement `p.OnEvent` to be told when a service changes status (`config` when the
  project is reloaded, `log` for every line: ask for it only if you need it).
- **Settings**: `p.OnInit(func(i pluginsdk.Init) error { return i.Settings(&cfg) })` reads what the user and the
  project gave the plugin; `i.ProjectDir`, `i.ConfigDir` and `i.StateDir` say where things are, and `i.Services` lists the services of the project
  (name, type and working directory), which is how a plugin finds the repository or the files of each one.

Requests are answered concurrently, so a slow call (`gh`, a web request) does not hold the others. A pane that takes
more than 10 seconds shows an error; an action may take up to a minute.

## Service sections, a column and service types

```yaml
contributes:
  serviceSections: [{ id: pr, title: Pull request }]   # a block in the detail of every service
  serviceColumn: { id: pr, width: 8 }                  # a column with the badges the plugin sets for the service
  serviceTypes: [{ name: k8s, badge: k8s }]
```

```go
p.Sections("pr", func(service string) ([]protocol.Section, error) { … })

p.ServiceType("k8s", pluginsdk.ServiceType{
    Validate: func(s pluginsdk.Service) error { … },                  // `orbit validate`
    Start:    func(s pluginsdk.Service) error { …; p.Log(s.Name, "…"); return nil },
    Stop:     func(s pluginsdk.Service) error { … },
})
```

`s.DecodeWith(&cfg)` reads the `with:` of the service. Output goes to `p.Log` (`p.LogError` for stderr); when the thing
stops by itself, `p.Exited(name, &code, "reason")`: orbit applies the `restart:` policy. If the plugin itself dies,
its services are marked as down.

## The command of the CLI

`cli: { command: [./orbit-weather] }` makes `orbit weather …` run it, with the arguments as typed, in the current
folder, and its exit code is orbit's. The environment has `ORBIT_BIN` (the running orbit: `orbit status --json`,
`orbit ctl restart api`… work from there), `ORBIT_PROJECT_DIR`, `ORBIT_PLUGIN_DIR`, `ORBIT_PLUGIN_CONFIG` and
`ORBIT_PLUGIN_STATE`.

## Publishing

`orbit plugin new` leaves a GoReleaser configuration and a GitHub Actions workflow. Push a tag and it publishes a
release with `orbit-<name>_<os>_<arch>.tar.gz` (`.zip` on Windows) holding the program and `orbit-plugin.yaml`, and a
`checksums.txt`: that is what `orbit plugin install owner/orbit-<name>` downloads. Add the topic `orbit-plugin` to the
repository, and open a pull request to the [list](community.md).

A plugin made of scripts (shell, Python, Node) needs no release: `orbit plugin install` clones the repository, and
`run` points at the script.

## Other languages

The protocol is JSON-RPC over the stdin and stdout of the plugin, one JSON object per line: see the
[protocol](protocol.md). The SDK is a convenience; nothing in orbit depends on it.

## Testing

`orbit plugin doctor` checks the manifest, the commands and the keys. The repository of orbit has a plugin that uses
everything, `test/fixtures/plugins/demo`, and tests that run it: read them for examples.
