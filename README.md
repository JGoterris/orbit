# orbit

TUI to launch, control and monitor local services —processes (backends, frontends,
workers), Docker containers and `docker compose` services— respecting their dependencies.
Built with TypeScript, [Bun](https://bun.sh) and [OpenTUI](https://github.com/anomalyco/opentui) (`@opentui/react`).

```
 ◉ orbit · demo     1 Dashboard  2 Graph  3 Logs                                ● 7 up  ○ 1
╭─ Services 7/8 ───────────╮╭─ Dependency graph · 7/8 up ────────────────────────────────────╮
│ ● postgres       :55432  ││                               ╭────────────────────╮           │
│ ● redis          :56379  ││                         ╭────▶│ ● auth             ├────╮      │
│ ● auth           :4300   ││ ╭────────────────────╮  │     │ healthy :4300 40M  │    │      │
│▌● api            :4100   ││ │ ● postgres         ├──┤     ╰────────────────────╯    │      │
│ ● worker                 ││ │ healthy :55432 19M │  │                               │      │
│ ● web            :4200   ││ ╰────────────────────╯  │                               │  ... │
```

## Features

- **Dependency graph** by layers (level 0 on the left), colored by status; selecting
  a node highlights its dependencies (cyan) and its dependents (purple).
  Navigable with arrow keys and mouse.
- **Ordered startup**: `start` brings up dependencies first and waits for them to be *ready*
  (healthcheck OK, or running if they don't have one); `stop` stops dependents first.
- **Healthchecks**: HTTP, TCP, command, or Docker's `HEALTHCHECK`. If you set `port` without
  `health`, a TCP check to that port is used.
- **Restart policy** `no | on-failure | always` with exponential backoff (1s → 30s).
- **Embedded console**: `i` opens `psql`, `rails console` or a container shell in a floating
  terminal (PTY + terminal emulator) without leaving orbit; it survives hiding the modal.
- **Watch mode**: `watch:` globs restart a service when its files change (for stacks without
  hot-reload: Go, Rust, plain backends). Changes are debounced and rate-limited (`cooldown`), so
  editing never causes a restart storm; `W` pauses it. Never starts a service you stopped.
  On WSL2, edits made from Windows tools to files under `/mnt/c` don't trigger inotify: keep the project in the Linux filesystem.
- **Long log lines wrap** (`w` toggles), the view stays put while you scroll up, and copy mode
  (`v`) selects across scrolling and copies whole lines via OSC 52 (or `wl-copy`/`xclip`/`pbcopy`/`clip.exe`).
- **Live logs** per service or combined with color prefix, regex filter, scroll
  (wheel/PgUp/PgDn), timestamps, error/warning highlighting.
- **CPU and memory** per process tree (via `/proc`) and per container (`docker stats`), with
  sparklines.
- **Docker / compose**: automatically imports `docker-compose.yml` (with `depends_on`, ports,
  healthchecks and `${VAR:-def}` interpolation), and re-attaches to containers that were
  already running. Those containers **are not stopped when exiting orbit** (only with explicit `x`/`X`).
- **Daemon**: the TUI is only a client. The services of a project run in an `orbit daemon` that the
  first `orbit` starts by itself (one per project, in its own session), so you can close the terminal, open
  another one (or tmux, or two at once) and find everything as it was, logs included. Health checks, restart
  policies, watchers and hooks keep working with no UI open. Closing the terminal never stops anything:
  on `q` choose *stop all* (the daemon quits too) or *leave running* (it keeps supervising).
  `orbit down` stops everything from outside. `orbit --no-daemon` runs the services inside the TUI instead:
  then *leave running* keeps the processes (own process group, output in
  `~/.local/state/orbit/<project>/logs/`) but nobody supervises them until the next `orbit` re-attaches.
- **`oneshot` tasks** (builds, migrations, provisioning): count as ready once they finish with
  exit code 0, are shown as `✓ done`, and aren't re-run when another dependent starts.
- Detects **occupied ports** before starting (and tells you which process is using them).
- Kills **entire process trees** (its own process group): no orphaned
  `vite`/`esbuild` processes hogging ports.
- Command palette with fuzzy search, service groups, open in browser.
- **Git, built in** (`4`): for every repo your services live in, changes, branches, commits and stashes in
  stacked panels with a diff next to them. Stage files or single **hunks**, commit, amend, discard (always asks), switch/create/delete
  branches, stash, fetch/pull/push. Side-by-side diffs with `s`. Branch and change count show in the
  header from any view. Conflicts, rebase and the like stay in lazygit (`L`).
- **Several projects, one orbit**: press `P` to switch to another project (pinned and recent ones, or
  type a folder path and `tab` to complete it). Services of the project you leave can be stopped or left
  running; opening it again re-attaches. Starting `orbit` in a folder that is not a project opens the picker.
- Press `L` to open the service's git repo in [lazygit](https://github.com/jesseduffield/lazygit)
  (if installed); orbit resumes when you quit it and services keep running meanwhile.

## Install

```bash
npm install -g @jgoterris/orbit
# or
bun add -g @jgoterris/orbit
# or, without installing:
bunx @jgoterris/orbit
```

Requires [Bun](https://bun.sh) ≥ 1.3 — orbit runs on Bun regardless of which package manager
installed it. Docker is only needed for `docker`/`compose` services. CPU/memory sparklines for
process trees read `/proc`, so they're Linux-only (container metrics via `docker stats` work
anywhere).

## Usage

```bash
orbit [dir]          # TUI using the orbit.yaml from dir (or a parent directory)
orbit open <project> # TUI for a remembered project, by name (or unique fragment) or path
orbit projects       # lists remembered projects (pinned first) and what is running in them
orbit --up           # TUI starting all autostart services
orbit --no-daemon    # TUI that runs the services itself (nothing keeps supervising after you quit)
orbit daemon [dir]   # the background supervisor on its own (the TUI starts it when needed; --idle <min> to auto-quit)
orbit up [svc|group] # without TUI: starts and shows logs (ctrl+c to stop)
orbit down           # stops whatever orbit left running; also quits an orbit that is open (TUI or `up`)
orbit status [--json]          # state of the services of a running orbit (TUI or `orbit up`)
orbit ctl start|stop|restart|toggle [svc|group…]   # drives a running orbit from another terminal
orbit graph          # prints the dependency graph
orbit ls             # lists services
orbit logs [svc|group…] [-f] [-n 200] [--grep re] [--since 10m]
                     # prints recent logs without the TUI (works while orbit is open or closed)
orbit init [dir]     # generates an orbit.yaml by scanning the project
```

Without `orbit.yaml`, orbit opens the services from a `docker-compose.yml` directly. In a folder with
neither, the TUI opens with no services and the project picker (other commands report an error).

Projects are remembered in `~/.config/orbit/projects.json` the first time orbit opens them (the 30 most
recent unpinned ones are kept; pinned ones are never dropped).

### Keys

| Key | Action |
| --- | --- |
| `↑↓` / `j k` | select service (in the graph, `←↑↓→` move spatially) |
| `space` | start / stop the selected one (with dependencies) |
| `s` `x` `r` | start / stop / restart the selected one |
| `S` `X` `R` | start / stop / restart everything |
| `W` | pause / resume file watching of the selected service |
| `1` `2` `3` | Dashboard / Graph / Logs |
| `tab` / `shift+tab` | move focus between panels |
| `z` · `esc` | zoom the focused panel to full screen · back |
| `+` `-` `=` | grow / shrink the focused panel · reset sizes |
| `j k` `ctrl+u/d` `g G` (logs focused) | scroll logs by line / half page / top / bottom |
| `enter` / `l` | logs of the selected one · `a` toggles selected ⇄ all |
| `/` | filter logs (regex) · `tab` switches to search (highlight, keep all lines) · `esc` clears |
| `n` · `N` | next (newer) · previous (older) search match |
| `f` · `PgUp PgDn` · wheel | follow / scroll logs |
| `t` · `w` · `c` | timestamps · line wrap · clear logs |
| `v` (logs focused) | copy mode: `j k` `ctrl+u/d` `g G` move, `v` start/clear selection, `y` copy, `esc` cancel |
| `Y` · `E` | copy all visible logs · export them to `~/.local/state/orbit/<project>/exports/` (the path is copied) |
| `o` | open `http://localhost:<port>` (or `url`) in the browser |
| `i` | interactive console of the selected service in a floating terminal (`console:` command, or `$SHELL` / the container's shell). `ctrl+]` hides it and keeps it running; `i` brings it back |
| `e` | environment variables of the selected service (secrets masked, `v` reveals) |
| `L` | open the selected service's git repo in [lazygit](https://github.com/jesseduffield/lazygit) (if installed) |
| `4` | Git view, see [Git](#git) |
| `P` | open another project: `enter` opens, `tab` completes a typed path, `ctrl+f` pins, `ctrl+x` forgets |
| `T` | change color theme (live preview, `enter` saves, `esc` cancels) |
| `:` / `ctrl+p` | command palette |
| `?` | help |
| `q` | quit: `s` stops everything, `d` leaves services running (reopen `orbit` to resume them) |

## Scripting & editors (socket)

While a project is running (its daemon, a TUI with `--no-daemon`, or `orbit up`) it listens on a local socket, so scripts and editor extensions can query
and drive it from outside: `~/.local/state/orbit/<project>/orbit.sock` (a shorter path under `$XDG_RUNTIME_DIR` or
`$TMPDIR` when that one would exceed the kernel's socket-path limit). The socket is `0600` inside a `0700` directory.
`orbit status --json`, `orbit ctl` and `orbit logs -f` are clients of it, and so is the TUI. The daemon writes
its own messages to `daemon.log` next to the socket.

Protocol: JSON-RPC 2.0, one JSON message per line (see `src/core/ipc/protocol.ts`).

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"snapshot"}' | socat - UNIX-CONNECT:$SOCK
```

Requests: `hello`, `snapshot`, `logs`, `start` / `stop` / `restart` / `toggle` (`{"services": [...]}`, groups expand),
`startAll`, `stopAll`, `toggleWatch`, `clearLogs`, `subscribe` (`{"logs": true}` to receive `state` and `log`
notifications) and `shutdown`. Linux, macOS and WSL2 use Unix sockets; the Windows endpoint (a named pipe) exists in
the code but orbit itself does not run on native Windows yet.

## Git

The Git view (`4`) works on the repositories your services live in: each service's working directory
is looked up, the project folder's own repo is included if it has one, and every repo appears once. With
more than one, a **Repos** panel at the top lists them (branch, ahead/behind, changed files, and which
services live there) and the one you pick drives all the other panels; the view opens on the repo of the
service you had selected, and the header sums up all of them. `tab` moves between Repos, Changes, Branches,
Commits, Stash and the Diff; the diff follows whatever is selected (a file, a branch against `HEAD`, a
commit, a stash). It refreshes every few seconds and after each action; git's own error message is
shown when something fails. Pull is fast-forward only, and network operations never ask for a
password (they fail instead; use an agent or a credential helper).

| Panel | Keys |
| --- | --- |
| everywhere | `f` fetch · `p` pull · `u` push (of the selected repo) · `F` fetch every repo · `j k` move · `g G` first / last · `z` zoom |
| Repos | `j k` pick the repo · `enter` go to its changes |
| Changes | `space` stage / unstage the file · `a` stage / unstage all · `c` commit · `A` amend (editing the subject keeps the body) · `d` discard (asks) · `s` stash · `v` staged ⇄ unstaged side · `y` / `Y` copy the path (relative / absolute) · `enter` open the diff |
| Branches | `enter` switch · `n` new branch · `d` delete (asks; unmerged work is refused) |
| Commits | `enter` browse the files the commit changed: `j k` pick one (the diff follows), `enter` focus its diff, `c` check the file out (its version from that commit goes into your working tree, shows up in Changes; asks first), `y` / `Y` copy its path, `esc` back to the commit list. Merge commits are compared with their first parent |
| Stash | `enter` browse its files, same keys as commits (`c` brings a file's stashed version back) · `space` apply · `o` pop · `d` drop (asks) |
| Diff | `esc` back to the panel (or file list) you came from · `{` `}` previous / next file without leaving the diff (in a commit, a stash or Changes) · `c` check the file out (commits and stashes) · `y` / `Y` copy its path · `j k` `ctrl+u/d` `g G` scroll · `[` `]` previous / next hunk · `space` stage hunk (unstage on the staged side) · `d` discard hunk (asks) · `s` side-by-side (read only, mouse wheel scrolls) |

Commit messages are one line for now; amending keeps the existing body.

## Themes

Press `T` (or `: Change theme…`) to pick one; the choice is saved in `~/.config/orbit/config.json` and applies to every project.

Built in: `orbit` (default), `catppuccin-mocha`, `catppuccin-macchiato`, `catppuccin-frappe`, `catppuccin-latte` (light), `tokyo-night`, `dracula`, `nord`, `gruvbox`, `rose-pine`, `one-dark`. Palettes come from the upstream projects (all MIT, see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)); orbit is not affiliated with them.

Your own themes are JSON files in `~/.config/orbit/themes/<name>.json` (`$XDG_CONFIG_HOME` is respected). They extend a built-in one (default `orbit`) and override any color:

```json
{
  "extends": "catppuccin-mocha",
  "colors": { "accent": "#ff79c6", "services": ["#ff79c6", "#8be9fd", "#50fa7b"] }
}
```

Colors are `#rrggbb`. Keys: `bg panel panelAlt selection cursor match border borderFocus text muted dim accent accent2 cyan green yellow orange red edge upstream downstream`, plus `services` (list used for log prefixes). Broken files are skipped and reported on startup.

## orbit.yaml

```yaml
name: my-stack
compose: ./docker-compose.yml   # optional; by default the compose in this directory is imported. false = don't import
env:                            # variables for all services. ${VAR} and ${VAR:-def} are interpolated (+ .env)
  LOG_LEVEL: info
env_file: .env.shared           # .env files for all services (optional)

services:
  api:                          # type: process (default if there is a cmd)
    cmd: bun run dev
    cwd: ./api
    env: { PORT: "3000" }
    env_file:                   # path, list of paths, or { path, required: false } if optional
      - ./api/.env
      - { path: ./api/.env.local, required: false }
    port: 3000                  # checks for occupied port, TCP health by default, `o` key
    depends_on: [postgres, redis]
    health: { http: "http://localhost:3000/health", interval: 2s, timeout: 2s }
    restart: on-failure         # no | on-failure | always
    watch: ["src/**", "package.json"]   # restart on change; globs relative to `cwd`
    # long form:
    # watch:
    #   paths: ["**/*.go", "go.mod"]
    #   ignore: ["**/*_test.go"]   # .git, node_modules and editor swap files are always ignored
    #   debounce: 1s               # quiet time needed after the last change (default 1s)
    #   cooldown: 10s              # at most one restart per cooldown; changes in between are batched (default 10s)
    console: bin/rails console  # `i` opens it in a terminal (process: in cwd/env; docker: inside the container)
    start_timeout: 60s
    stop_timeout: 8s

  redis:                        # type: docker (if there is an image)
    image: redis:7-alpine
    ports: ["6379:6379"]
    volumes: ["./data:/data"]
    cmd: redis-server --appendonly yes
    console: redis-cli          # `docker exec -it`; without `console:` you get bash/sh

  postgres:                     # defined in docker-compose.yml: only fields overridden here
    restart: always

  docs:
    cmd: bun run docs
    autostart: false            # not started with S / --up

  build-lib:                    # task: build, migration, provisioning…
    cmd: mvn -q install -pl shared-kernel -am -DskipTests
    oneshot: true               # "ready" when it finishes with exit code 0; its dependents wait

  api:
    cmd: bun run dev
    pre_start: "rm -rf tmp/*"   # before launching; if it fails the service is `failed`
    post_start:                 # once ready; a failure is only reported
      - ./scripts/check-seed.sh
      - { cmd: "curl -fsS localhost:3000/warmup", timeout: 10s }
    post_stop: "echo cleaned"   # after it goes down, however it went down

groups:
  backend: [postgres, redis, api]
```

**Lifecycle hooks.** `pre_start`, `post_start` and `post_stop` take a command, a `{ cmd, timeout }` or a
list of them (run in order, stopping at the first failure; default timeout 60s). They run on the host in the
service's `cwd`, with its environment plus `ORBIT_SERVICE`, `ORBIT_HOOK` and, in `post_stop` after the
service exited on its own, `ORBIT_EXIT_CODE`; their output goes to the service's log. They work on
`process`, `docker` and `compose` services.

- `pre_start` runs before every start (restarts included). If it fails, the service is `failed` and doesn't start.
- `post_start` runs once the service is ready and never blocks its dependents; if it fails it is logged and
  shown as the service's error, but the service stays up. Not allowed on `oneshot` tasks.
- `post_stop` runs whenever the service goes down: stopped, restarted, crashed or a finished `oneshot`. A
  restart waits for it. It doesn't run when orbit detaches or leaves an adopted container running.

**Environment variables.** A service receives, from lowest to highest priority: your shell's
environment, the global `env_file`s, the service's `env_file`s (in order: the last one wins), and then the global
`env` and the service's `env` (inline values always win over files). Paths are relative to
`orbit.yaml`. Files are **re-read on every startup**, so after editing an `.env`
it's enough to restart the service (`r`). A required file that doesn't exist is an error when loading
the config and when starting. The service detail shows which files it uses and the log indicates how many
variables were loaded. In `compose` services, the environment is managed by compose itself (`env_file`
inside the `docker-compose.yml`).

The `.env` next to `orbit.yaml` (and the one next to each compose) is used to interpolate `${VAR}`
in the configuration, just like in docker compose. It is not injected into the services unless you
put it in `env_file`.

`health` accepts shortcuts: `"http://…"`, `"tcp:5432"`, `"container"` or a command (`"pg_isready"`).

## Structure

```
src/
  index.tsx            CLI + renderer startup
  cli.ts               up / down / status / ctl / graph / ls / init (daemon: core/ipc/daemon.ts)
  config/              schema, orbit.yaml loading, compose import
  core/
    graph.ts           DAG: cycles, topological order, levels
    supervisor.ts      states, dependencies, healthchecks, restarts, metrics
    watch.ts           file watcher with debounce + cooldown (watch mode)
    runners.ts         process / docker / compose
    userConfig.ts      ~/.config/orbit: saved theme, custom themes
    projects.ts        project registry (projects.json), status, path completion
    session.ts         switching the open project
    ipc/               local socket: server (wraps a supervisor), client, JSON-RPC protocol, endpoint path,
                       remote.ts (RemoteSupervisor: the TUI's mirror of a daemon), daemon.ts (orbit daemon + auto-start)
    git/               status, diff + hunk parsing, operations, GitRepo (cached state + change events),
                       repos.ts (the repos a project's services live in)
    health.ts metrics.ts logs.ts exec.ts
  ui/
    App.tsx            layout, keyboard, views, overlays
    graphLayout.ts     graph layout by layers (pure, testable)
    ProjectHost.tsx    keeps the open project; remounts App when it changes
    views/             view registry (Dashboard, Graph, Logs, Git): label, panes, render, hints, own keys
    DiffPane.tsx ListPanel.tsx diffRows.ts   diff and list panels of the Git view
    GraphView.tsx ServiceList.tsx ServiceDetail.tsx LogView.tsx Overlays.tsx theme.ts themes.ts hooks.ts
test/                  bun test (config, graph, supervisor with real processes, TUI with test renderer)
```

## Development

```bash
bun install
bun test
bun run typecheck

bun link            # installs the `orbit` command globally from this checkout
```
