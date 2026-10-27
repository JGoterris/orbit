# Plugins

Plugins add to orbit: a new tab, a block in the detail of a service, a column in the list of services, badges in
the header, commands in the palette, even new values for `type:` in `orbit.yaml`. They look like the rest of orbit
because orbit draws them: a plugin hands over data (rows, fields, text) and orbit shows it with its own lists,
colours, keys and mouse.

A plugin is a program, in any language, that orbit starts and talks to. If it crashes, orbit carries on and shows
why. To write one, see [Writing a plugin](authoring.md).

!!! warning "A plugin runs with your permissions"
    orbit does not sandbox plugins. They can read your files and run commands, like any program you install. Install
    only the ones you trust, and read what `orbit plugin install` tells you it will run before you say yes.

## Install

```bash
orbit plugin install owner/orbit-forge          # the latest release of a GitHub repository
orbit plugin install owner/orbit-forge@v0.2.0   # a version
orbit plugin install ./my-plugin                # a folder (copied)
```

`orbit plugin install` shows what the plugin is, where it comes from, the commands it will run and what it adds to
orbit, and asks. `-y` skips the question.

From GitHub, orbit downloads the archive of your platform from the release (`orbit-<name>_<os>_<arch>.tar.gz`, `.zip`
on Windows) and checks it against the `checksums.txt` of the release. A repository without a release for your platform
is cloned instead; that is only good for plugins made of scripts, and orbit tells you nothing was verified.

| Command | |
| --- | --- |
| `orbit plugin ls [--json]` | installed plugins |
| `orbit plugin upgrade [name]` | the latest version of one plugin, or of all of them |
| `orbit plugin remove <name> [--purge]` | uninstall; `--purge` also deletes its settings, its data and what `config.json` says about it |
| `orbit plugin enable <name>` · `disable <name>` | turn a plugin on or off for you |
| `orbit plugin doctor [name]` | check manifests, commands, keys and service types |
| `orbit plugin logs <name> [-n lines] [-f]` | what the process of a plugin printed |
| `orbit plugin link <folder>` | use a folder as a plugin without copying it, to [develop](authoring.md) one |
| `orbit plugin dir [name]` | where they are installed |

Plugins are installed for you, not for a project, in `~/.local/share/orbit/plugins` (`$XDG_DATA_HOME`, or
`%LOCALAPPDATA%` on Windows). A plugin takes effect the next time orbit starts; an orbit daemon that is already
running keeps the plugins it started with until `orbit down`.

## Using them

- **Tabs** are in the header after the ones of orbit, with their number: press it, or pick *View: …* in the palette.
  They work like the Git view: `tab` moves between panes, `z` zooms, `j`/`k` move, `/` filters a list, the mouse works,
  `ctrl+r` asks the plugin again.
- **Commands** are in the palette (`:`), prefixed with the name of the plugin, and in `?`. The ones that have a key
  show it in the footer when they apply. An action may ask for something first (a choice, some text) or for a
  confirmation; they are drawn like orbit's own dialogs.
- **Badges** appear in a column of the list of services, in the detail of a service and in the header; if a badge in
  the header has an action, clicking it runs it.
- **Service types**: a plugin can provide values for `type:`, with their own settings in `with:`:

  ```yaml
  services:
    pods:
      type: k8s
      with: { context: staging, namespace: shop }
  ```

  orbit still handles dependencies, health checks, restarts and logs; the plugin starts and stops the thing. These
  services stop when orbit quits: they live in the plugin, which does not outlive orbit.
- **Commands of the CLI**: a plugin may have its own, run as `orbit <plugin> …`. The arguments go to the plugin as you
  type them, so `orbit forge mr --state open` is for the plugin to interpret. If there is also a folder called `forge`,
  `orbit forge` on its own opens the folder (`orbit ./forge` always does); with arguments it is the plugin.

If a plugin fails, its tab says why and the header shows `✖ plugin <name>`; `orbit plugin logs <name>` has the rest.
orbit starts it again up to three times. To open orbit without any plugin (to see whether one is the problem), set
`ORBIT_NO_PLUGINS=1`.

## Per project

`orbit.yaml` can say which plugins the project uses:

```yaml
plugins:
  forge: true                  # the project needs it: orbit warns when it is not installed
  kube: { context: staging }   # the same, with settings for the plugin
  noisy: false                 # off in this project
```

orbit never installs anything by itself: a missing plugin is a warning in the header and a message with the command
to run. Changes to `plugins:` apply the next time orbit starts for the project.

## Per user

`~/.config/orbit/config.json` can turn plugins off, give them settings and change their keys:

```json
{
  "plugins": {
    "forge": { "enabled": true, "settings": { "host": "gitlab.example.com" } },
    "noisy": { "enabled": false }
  },
  "keys": { "forge.merge": "ctrl+m", "forge.open": "none" }
}
```

`orbit plugin enable|disable` write the first part. The settings of the project win over yours, key by key. A key is
`<plugin>.<action>`; `"none"` removes it. An action cannot take a key orbit uses itself, nor one that another action
of the same place has: it keeps its entry in the palette, and `orbit plugin doctor` tells you.

## Finding plugins

GitHub repositories with the topic [`orbit-plugin`](https://github.com/topics/orbit-plugin), and the
[curated list](community.md).
