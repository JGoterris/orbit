# Manifest reference

`orbit-plugin.yaml` at the root of the plugin. Unknown keys are an error. Editors can check it with the
[schema](https://github.com/JGoterris/orbit/blob/main/orbit-plugin.schema.json):

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/JGoterris/orbit/main/orbit-plugin.schema.json
```

```yaml
name: forge               # lowercase letters, digits, dashes; also the command `orbit forge`
version: 0.1.0
api: 1                    # the version of the protocol the plugin speaks
minOrbit: 1.2.0           # optional: the oldest orbit that can run it
description: Merge requests and CI
run: { command: [./orbit-forge, serve], windows: [./orbit-forge.exe, serve] }
cli: { command: [./orbit-forge] }
events: [state]           # state | config | log
contributes:
  views: […]
  actions: […]
  serviceSections: […]
  serviceColumn: { id: pr, width: 8 }
  headerBadges: true
  serviceTypes: […]
```

`run` and `cli` are commands without a shell: a list of arguments, where paths that start with `./` are in the folder of
the plugin. `windows` replaces the command on Windows. A plugin that contributes anything to the interface needs
`run`; one with only `cli` is just a command.

The name cannot be one of orbit's own commands.

## views

```yaml
views:
  - id: prs
    title: Pull requests          # the label of the tab
    sidebar: services             # none (default) | services: the list of services on the left, like Graph
    panes:
      - { id: list, widget: list, title: MR, weight: 2 }
      - { id: info, widget: detail, title: Details, bind: list }
      - { id: log, widget: text, title: Log, bind: list, follow: true }
```

One to six panes. The first is on the left and the rest are stacked on its right, sharing the height by `weight`
(default 1). On a terminal narrower than 70 columns one pane shows at a time (`tab` moves). `widget` is `list`,
`detail` or `text`. `bind: <pane>` (a pane declared before) makes it follow: it is asked again whenever the selection of
that pane changes, and waits until there is one. `follow: true` keeps a text pane at its end, `wrap: true` breaks its
long lines instead of cutting them (a detail always does). `sized: true` makes orbit ask again for the data of the pane
when its width changes, with the width in `context.width`: for a pane that lays out what it draws, such as a graph.

`enter` on a list, or on a pane bound to it, opens the selected item full screen: the panes bound to the list, one over
the other, with the whole width. From a pane bound to a list, full screen or not, `[` and `]` select the previous and
next item of the list without going back to it; `esc` leaves the full screen. An action of a pane (`scope.pane`) also
runs from the panes bound to it, on the item they show.

## actions

```yaml
actions:
  - id: merge
    title: Merge
    key: M
    scope: { view: prs, pane: list }
    confirm: { title: "Merge {item} with {method}?", danger: true }
    inputs:
      - { name: method, kind: pick, options: [squash, merge, rebase] }
```

- **`scope`**: nothing (anywhere), `view` (and `pane`) or `service: true` (one palette entry per service; the key
  runs it on the selected one). An action of a view is not offered elsewhere.
- **`key`**: one key (`M`, `enter`, `ctrl+g`) or `alt+<key>` / `f1`-`f12`. An action of no view works in every view, so
  its key has to be `alt+…` or a function key. The user can change it ([Plugins](index.md#per-user)). orbit keeps the
  keys it uses itself (`q`, `:`, `?`, `tab`, `z`, `esc`, `p`, `P`, `T`, `U`, `!`, `+`, `-`, `=`, the digits) and the
  ones of the widgets (`j`, `k`, `g`, `G`, `/`, `ctrl+d`, `ctrl+u`, `ctrl+r`, `[`, `]`, arrows, `home`, `end`, `pgup`,
  `pgdown`). `enter` is not kept: an action of the view, or of the list, that takes it runs instead of opening the item
  full screen.
- **`inputs`** are asked in order before the confirmation: `text` (with `default`), or `pick` from `options`, or with
  `optionsFrom: true` from the plugin (`action.options`).
- **`confirm`** asks yes / no. In its `title`, `{item}` is the selected row of the pane (its first column), `{service}`
  the selected service and `{<name>}` an answer given before.
- **`item: true`** says the action is about the selected item of its pane (`scope.pane`): while the list has none, it
  is not in the hints and its key does nothing but say so.

## serviceSections, serviceColumn, headerBadges

`serviceSections` is a list of `{ id, title }`: blocks of the detail of every service, asked for with
`service.sections`. `serviceColumn` is a column of the list of services with the badges the plugin sets for each one
(`width`, 3 or more); it shows if there is room, before the memory, cpu and type columns. `headerBadges: true` lets the
plugin show badges in the header. A plugin can only badge what its manifest declares.

## serviceTypes

```yaml
serviceTypes:
  - { name: k8s, badge: k8s }     # badge: up to four letters, next to the services of the type
```

The name cannot be `process`, `docker`, `compose` or `external`. If two installed plugins provide the same type,
`orbit plugin doctor` says so and the first one is used.
