# Usage

```bash
orbit --version      # prints the version
orbit [dir]          # TUI using the orbit.yaml from dir (or a parent directory)
orbit open <project> # TUI for a remembered project, by name (or unique fragment) or path
orbit projects       # lists remembered projects (pinned first) and what is running in them
orbit --up           # TUI starting all autostart services
orbit --no-daemon    # TUI that runs the services itself (nothing keeps supervising after you quit)
orbit daemon [dir]   # the background supervisor on its own (the TUI starts it when needed; --idle <min> to auto-quit)
orbit up [svc|group] # without TUI: starts and shows logs (ctrl+c to stop)
orbit up -d [svc|group]        # starts them in the background daemon and returns once they are ready
orbit down           # stops whatever orbit left running; also quits an orbit that is open (TUI or `up`)
orbit status [--json]          # state of the services of a running orbit (TUI or `orbit up`)
orbit ctl start|stop|restart|toggle [svc|group…]   # drives a running orbit from another terminal
                     # (restart of a group: only its members that are up; a service named alone starts if down)
                     # (with nothing running, start / restart / toggle start the daemon first)
orbit ctl reload     # applies the edits made to orbit.yaml to a running orbit
orbit graph          # prints the dependency graph
orbit ls             # lists services (and tasks, and profiles)
orbit run [task] [-- args]   # runs a task of orbit.yaml on this terminal (no task: lists them)
orbit logs [svc|group…] [-f] [-n 200] [--grep re] [--since 10m] [--json]
                     # prints recent logs without the TUI (works while orbit is open or closed: open, the
                     # streams come interleaved; closed, from the log files, stdout first and then stderr);
                     # --json prints one {"service","stream","text"} object per line ("ts" too while orbit runs)
orbit update [--check]         # updates orbit through the channel it was installed with (see Updating below)
orbit plugin install|link|ls|upgrade|remove|enable|disable|doctor|logs|dir|new   # plugins: see Plugins
orbit <plugin> [args…]         # the command of a plugin, with the arguments as typed (`orbit plugin ls` lists them)
orbit init [dir]     # generates an orbit.yaml by scanning the project
orbit validate [dir] # checks the orbit.yaml (errors and unknown keys, e.g. a typo like `helth:`); exit code 1 if invalid

orbit -c <file>      # (any command) use that config file instead of looking for orbit.yaml
orbit --profile debug[,…] # (any command) activate profiles of orbit.yaml; the TUI, `up` and `ctl` remember them. --no-profiles clears
```

Without `orbit.yaml`, orbit opens the services from a `docker-compose.yml` directly. In a folder with
neither, the TUI opens with no services and the project picker (other commands report an error).

The TUI opens with a short animation of the logo; any key skips it. To turn it off, set `"splash": false` in
`~/.config/orbit/config.json` (it is also left out with `NO_COLOR`).

## Updating

`orbit update` updates orbit through the channel it was installed with:

- **Homebrew**: `brew update` and `brew upgrade JGoterris/tap/orbit` (the full name, because other packages are called orbit).
- **go install**: `go install github.com/JGoterris/orbit/cmd/orbit@latest`.
- **apt / rpm**: prints `sudo apt update && sudo apt install --only-upgrade orbit` (or the dnf one); orbit never runs sudo.
  If the package that owns the binary is not the one from JGoterris, nothing is touched.
- **a binary from GitHub Releases**: downloads the archive of your platform, checks it against `checksums.txt` and
  replaces the executable.

`orbit update --check` only reports whether there is a newer version (exit code 1 if so). A daemon that is already
running keeps the old version until `orbit down`.

The TUI shows a toast when a newer release exists. It asks GitHub at most once a day, in the background, and only the
TUI does (`up`, `logs`, `status`... never). To turn it off, set `"updateCheck": false` in `~/.config/orbit/config.json`
or define `ORBIT_NO_UPDATE_CHECK`.

See [keys.md](keys.md) for the keyboard shortcuts.

## Editing `orbit.yaml` while running

When `orbit.yaml` (or its [`orbit.local.yaml`](configuration.md#local-overrides-orbitlocalyaml) override) changes, the header shows `⟳ orbit.yaml changed · U`; `U` shows the diff and applies it on `y`.
Like `docker compose up`, only what changed is touched: modified services restart (dependents don't), removed
ones stop, added ones start if `autostart`. `orbit` / `orbit up` on a running project and `orbit ctl reload` do the same.

Projects are remembered in `~/.config/orbit/projects.json` the first time orbit opens them (the 30 most
recent unpinned ones are kept; pinned ones are never dropped).
